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

package pan

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"testing"

	"github.com/scionproto/scion/go/lib/addr"
	"github.com/scionproto/scion/go/lib/slayers/path"
	"github.com/scionproto/scion/go/lib/slayers/path/scion"
	"github.com/scionproto/scion/go/lib/snet"
	snetpath "github.com/scionproto/scion/go/lib/snet/path"
	"github.com/scionproto/scion/go/lib/xtest"
)

func benchmarkFilterPacket(b *testing.B, path snetpath.SCION) {
	conn := setupConn()
	pkt := snet.Packet{
		PacketInfo: snet.PacketInfo{
			Source: snet.SCIONAddress{
				IA: xtest.MustParseIA("1-ff00:0:112"),
			},
		},
	}
	fp := ForwardingPath{
		dataplanePath: path,
	}
	fprint := fp.Fingerprint()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		conn.FilterPacket(pkt, fprint)
	}
}

func BenchmarkFilterPacketLong(b *testing.B) {
	benchmarkFilterPacket(b, setupPacketLong())
}
func BenchmarkFilterPacketThreeSeg(b *testing.B) {
	benchmarkFilterPacket(b, setupPacketThreeSeg())
}
func BenchmarkFilterPackeShort(b *testing.B) {
	benchmarkFilterPacket(b, setupPacketShort())
}

// func benchmarkCompleteFilterPacket(b *testing.B, path snetpath.SCION) {
// 	conn := setupConn()
// 	pkt := snet.Packet{
// 		PacketInfo: snet.PacketInfo{
// 			Source: snet.SCIONAddress{
// 				IA: xtest.MustParseIA("1-ff00:0:112"),
// 			},
// 		},
// 	}
// 	fp := ForwardingPath{
// 		dataplanePath: path,
// 	}
// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		fprint := fp.Fingerprint()
// 		conn.FilterPacket(pkt, fprint)
// 	}
// }

// func BenchmarkCompleteFilterPacketLong(b *testing.B) {
// 	benchmarkCompleteFilterPacket(b, setupPacketLong())
// }
// func BenchmarkCompleteFilterPacketThreeSeg(b *testing.B) {
// 	benchmarkCompleteFilterPacket(b, setupPacketThreeSeg())
// }
// func BenchmarkCompleteFilterPacketShort(b *testing.B) {
// 	benchmarkCompleteFilterPacket(b, setupPacketShort())
// }

func setupConn() baseUDPConn {

	return baseUDPConn{
		allowedPaths: readACL("test_data/acl.json"),
	}
}

func setupPacketLong() snetpath.SCION {
	dec := scion.Decoded{
		Base: scion.Base{
			PathMeta: scion.MetaHdr{
				SegLen: [3]uint8{63, 63, 63},
			},
			NumINF:  3,
			NumHops: 189,
		},
		InfoFields: make([]path.InfoField, 3),
		HopFields:  make([]path.HopField, 189),
	}

	spath, err := snetpath.NewSCIONFromDecoded(dec)
	if err != nil {
		panic(err)
	}
	return spath
}

func setupPacketThreeSeg() snetpath.SCION {
	dec := scion.Decoded{
		Base: scion.Base{
			PathMeta: scion.MetaHdr{
				SegLen: [3]uint8{2, 2, 2},
			},
			NumINF:  3,
			NumHops: 6,
		},
		InfoFields: make([]path.InfoField, 3),
		HopFields:  make([]path.HopField, 6),
	}

	spath, err := snetpath.NewSCIONFromDecoded(dec)
	if err != nil {
		panic(err)
	}
	return spath
}

func setupPacketShort() snetpath.SCION {
	dec := scion.Decoded{
		Base: scion.Base{
			PathMeta: scion.MetaHdr{
				SegLen: [3]uint8{2, 0, 0},
			},
			NumINF:  1,
			NumHops: 2,
		},
		InfoFields: make([]path.InfoField, 1),
		HopFields:  make([]path.HopField, 2),
	}

	spath, err := snetpath.NewSCIONFromDecoded(dec)
	if err != nil {
		panic(err)
	}
	return spath
}

func readACL(pathToFile string) map[addr.IA][]PathFingerprint {
	if pathToFile == "" {
		panic("WARNING: Not ACL file provided. Accepting any paths...")
	}
	file, err := os.Open(pathToFile)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	rawFile, err := ioutil.ReadAll(file)
	if err != nil {
		panic(ErrNoPath)
	}
	var acl map[addr.IA][]PathFingerprint
	err = json.Unmarshal(rawFile, &acl)
	if err != nil {
		panic(err)
	}
	return acl
}
