// Copyright 2021 ETH Zurich
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

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"time"

	"github.com/lucas-clemente/quic-go"
	slog "github.com/scionproto/scion/go/lib/log"
	"gopkg.in/alecthomas/kingpin.v2"
	"inet.af/netaddr"

	"github.com/netsec-ethz/scion-apps/pkg/pan"
	"github.com/netsec-ethz/scion-apps/pkg/quicutil"
)

func main() {
	var localAddr *net.TCPAddr
	pathToFile := kingpin.Flag("acl", "Path to Path-based Access Control List").Default("").String()
	kingpin.Flag("addr", "Local addr to translate to SCION").Required().TCPVar(&localAddr)
	kingpin.Parse()

	logCfg := slog.Config{Console: slog.ConsoleConfig{Level: "debug"}}
	if err := slog.Setup(logCfg); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %s\n", err)
		os.Exit(2)
	}

	acl, err := readACL(*pathToFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %s\n", err)
		os.Exit(2)
	}

	// Proxy HTTPS, forward the entire TLS traffic data
	log.Fatalf("%s", forwardTLS(localAddr.String(), acl))
}

func readACL(pathToFile string) ([]pan.PathFingerprint, error) {
	if pathToFile == "" {
		slog.Info("WARNING: Not ACL file provided. Accepting any paths...")
		return nil, nil
	}
	file, err := os.Open(pathToFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	rawFile, err := ioutil.ReadAll(file)
	if err != nil {
		return nil, err
	}
	var acl map[string][]pan.PathFingerprint
	err = json.Unmarshal(rawFile, &acl)
	if err != nil {
		return nil, err
	}
	slog.Info("read ACL on", "pathToFile", pathToFile)
	slog.Debug("ACL", "paths", acl["paths"])
	return acl["paths"], nil
}

// forwardTLS listens on 443 and forwards each sessions to the corresponding
// TCP/IP host identified by SNI
func forwardTLS(addrStr string, acl []pan.PathFingerprint) error {
	addr, err := netaddr.ParseIPPort(addrStr)
	if err != nil {
		return err
	}
	listener, err := listen(addr, acl)
	fmt.Printf("server listenning on:  %v\n", listener.Addr())
	if err != nil {
		return err
	}
	for {
		sess, err := listener.Accept(context.Background())
		if err != nil {
			return err
		}
		go forwardTLSSession(sess)
	}

}

// forwardTLS forwards traffic for sess to the corresponding TCP/IP host
// identified by SNI.
func forwardTLSSession(sess quic.Session) {
	clientConn, err := quicutil.NewSingleStream(sess)
	if err != nil {
		return
	}
	dstConn, err := net.Dial("tcp", "127.0.0.1:443")
	if err != nil {
		logForwardTLS(sess.RemoteAddr(), 503)
		_ = sess.CloseWithError(503, "service unavailable")
		return
	}

	logForwardTLS(sess.RemoteAddr(), 200)
	go transfer(dstConn, clientConn)
	transfer(clientConn, dstConn)
}

// logForwardTLS logs TLS forwarding in something similar to the Common Log
// Format, as used by the LoggingHandler above.
// Status is a code that is part to the log line. This is not HTTP, but we
// (re-)use the HTTP codes with a similar meaning.
func logForwardTLS(client net.Addr, status int) {
	ts := time.Now().Format("02/Jan/2006:15:04:05 -0700")
	fmt.Printf("%s - - [%s] \"TUNNEL \" %d -\n", client, ts, status)
}

func transfer(dst io.WriteCloser, src io.ReadCloser) {
	defer dst.Close()
	defer src.Close()
	buf := make([]byte, 1024)
	var written int64
	for {
		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := dst.Write(buf[0:nr])
			if nw < 0 || nr < nw {
				nw = 0
				if ew == nil {
					ew = errors.New("invalid write")
				}
			}
			written += int64(nw)
			if ew != nil || nr != nw {
				break
			}
		}
		if er != nil {
			break
		}
	}
}

func listen(laddr netaddr.IPPort, allowedPaths []pan.PathFingerprint) (quic.Listener, error) {
	tlsCfg := &tls.Config{
		NextProtos:   []string{quicutil.SingleStreamProto},
		Certificates: quicutil.MustGenerateSelfSignedCert(),
	}
	return pan.ListenQUIC(context.Background(), laddr, nil, allowedPaths, tlsCfg, nil)
}
