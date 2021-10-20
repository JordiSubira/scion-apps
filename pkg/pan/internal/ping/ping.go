// Copyright 2020 Anapaya Systems, ETH Zurich
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ping

// XXX this is copy-pasted & adapted from github.com/scionproto/scion/go/pkg/ping
// Adapted to allow pinging multiple, changing destination (or one destination over multiple paths)
// from the same socket.
// Getting any changes upstreamed is unlikely at the moment as there are no
// resources to review PRs, sadly.

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand"
	"net"
	"time"

	"github.com/scionproto/scion/go/lib/addr"
	"github.com/scionproto/scion/go/lib/common"
	"github.com/scionproto/scion/go/lib/serrors"
	"github.com/scionproto/scion/go/lib/snet"
	"github.com/scionproto/scion/go/lib/sock/reliable"
	"github.com/scionproto/scion/go/lib/spath"
	"github.com/scionproto/scion/go/lib/topology/underlay"
)

type Pinger struct {
	Replies <-chan Reply

	// errHandler is invoked for every error that does not cause pinging to
	// abort. Execution time must be small, as it is run synchronously.
	errHandler func(error)

	id    uint64
	conn  snet.PacketConn
	local *snet.UDPAddr
	pld   []byte
}

func NewPinger(ctx context.Context,
	dispatcher reliable.Dispatcher,
	local *snet.UDPAddr,
) (*Pinger, error) {

	id := rand.Uint64()
	replies := make(chan Reply, 10)

	svc := snet.DefaultPacketDispatcherService{
		Dispatcher: dispatcher,
		SCMPHandler: scmpHandler{
			id:      uint16(id),
			replies: replies,
		},
	}
	conn, port, err := svc.Register(ctx, local.IA, local.Host, addr.SvcNone)
	if err != nil {
		return nil, err
	}

	local = local.Copy()
	local.Host.Port = int(port)

	return &Pinger{
		Replies:    replies,
		errHandler: nil,
		id:         id,
		conn:       conn,
		local:      local,
		pld:        make([]byte, 8), // min payload size
	}, nil
}

func (p *Pinger) Send(ctx context.Context, remote *snet.UDPAddr,
	sequence uint16, size int) error {

	// we need to have at least 8 bytes to store the request time in the
	// payload.
	if size < 8 {
		size = 8
	}
	if cap(p.pld) < size {
		p.pld = make([]byte, size)
	}
	binary.BigEndian.PutUint64(p.pld[:size], uint64(time.Now().UnixNano()))
	pkt, err := pack(p.local, remote, snet.SCMPEchoRequest{
		Identifier: uint16(p.id),
		SeqNumber:  sequence,
		Payload:    p.pld[:size],
	})
	if err != nil {
		return err
	}
	nextHop := remote.NextHop
	if nextHop == nil && p.local.IA.Equal(remote.IA) {
		nextHop = &net.UDPAddr{
			IP:   remote.Host.IP,
			Port: underlay.EndhostPort,
			Zone: remote.Host.Zone,
		}
	}
	if err := p.conn.WriteTo(pkt, nextHop); err != nil {
		return err
	}
	return nil
}

func (p *Pinger) Drain(ctx context.Context) {
	var last time.Time
	for {
		select {
		case <-ctx.Done():
			return
		default:
			var pkt snet.Packet
			var ov net.UDPAddr
			if err := p.conn.ReadFrom(&pkt, &ov); err != nil && p.errHandler != nil {
				// Rate limit the error reports.
				if now := time.Now(); now.Sub(last) > 500*time.Millisecond {
					p.errHandler(serrors.WrapStr("reading packet", err))
					last = now
				}
			}
		}
	}
}

func (p *Pinger) Close() error {
	return p.conn.Close()
}

type Reply struct {
	Received time.Time
	Source   snet.SCIONAddress
	Path     spath.Path
	Size     int
	Reply    snet.SCMPEchoReply
	Error    error
}

func (r *Reply) RTT() time.Duration {
	return r.Received.Sub(time.Unix(0, int64(binary.BigEndian.Uint64(r.Reply.Payload)))).
		Round(time.Microsecond)
}

type ExternalInterfaceDownError struct {
	snet.SCMPExternalInterfaceDown
	Path spath.Path
}

func (e ExternalInterfaceDownError) Error() string {
	return fmt.Sprintf("external interface down %s %d", e.IA, e.Interface)
}

type InternalConnectivityDownError struct {
	snet.SCMPInternalConnectivityDown
	Path spath.Path
}

func (e InternalConnectivityDownError) Error() string {
	return fmt.Sprintf("internal connectivity down %s %d %d", e.IA, e.Ingress, e.Egress)
}

type scmpHandler struct {
	id      uint16
	replies chan<- Reply
}

func (h scmpHandler) Handle(pkt *snet.Packet) error {
	echo, err := h.handle(pkt)
	h.replies <- Reply{
		Received: time.Now(),
		Source:   pkt.Source,
		Path:     pkt.Path,
		Size:     len(pkt.Bytes),
		Reply:    echo,
		Error:    err,
	}
	return nil
}

func (h scmpHandler) handle(pkt *snet.Packet) (snet.SCMPEchoReply, error) {
	if pkt.Payload == nil {
		return snet.SCMPEchoReply{}, serrors.New("no v2 payload found")
	}
	switch s := pkt.Payload.(type) {
	case snet.SCMPEchoReply:
	case snet.SCMPExternalInterfaceDown:
		return snet.SCMPEchoReply{}, ExternalInterfaceDownError{s, pkt.Path}
	case snet.SCMPInternalConnectivityDown:
		return snet.SCMPEchoReply{}, InternalConnectivityDownError{s, pkt.Path}
	default:
		return snet.SCMPEchoReply{}, serrors.New("not SCMPEchoReply",
			"type", common.TypeOf(pkt.Payload),
		)
	}
	r := pkt.Payload.(snet.SCMPEchoReply)
	if r.Identifier != h.id {
		return snet.SCMPEchoReply{}, serrors.New("wrong SCMP ID",
			"expected", h.id, "actual", r.Identifier)
	}
	return r, nil
}

func pack(local, remote *snet.UDPAddr, req snet.SCMPEchoRequest) (*snet.Packet, error) {
	if remote.Path.IsEmpty() && !local.IA.Equal(remote.IA) {
		return nil, serrors.New("no path for remote ISD-AS", "local", local.IA, "remote", remote.IA)
	}
	pkt := &snet.Packet{
		PacketInfo: snet.PacketInfo{
			Destination: snet.SCIONAddress{
				IA:   remote.IA,
				Host: addr.HostFromIP(remote.Host.IP),
			},
			Source: snet.SCIONAddress{
				IA:   local.IA,
				Host: addr.HostFromIP(local.Host.IP),
			},
			Path:    remote.Path,
			Payload: req,
		},
	}
	return pkt, nil
}
