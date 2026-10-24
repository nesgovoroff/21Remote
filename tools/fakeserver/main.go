// SPDX-License-Identifier: AGPL-3.0-or-later
// fakeserver imitates a serial device server with a D21m frame on its serial
// port, for testing network connections without hardware.
//
//	go run ./tools/fakeserver -listen :4001          (TCP Server mode)
//	go run ./tools/fakeserver -connect 10.0.0.5:4001  (TCP Client mode)
package main

import (
	"flag"
	"log"
	"net"
	"time"

	"remote21/internal/core"
)

func bridge(c net.Conn, sim *core.SimPort) {
	defer c.Close()
	log.Printf("connected: %s", c.RemoteAddr())
	b := make([]byte, 256)
	for {
		n, err := c.Read(b)
		if err != nil {
			log.Printf("disconnected: %v", err)
			return
		}
		sim.Write(b[:n])
		out := make([]byte, 512)
		for {
			k, _ := sim.ReadTimeout(out, 3*time.Millisecond)
			if k == 0 {
				break
			}
			c.Write(out[:k])
		}
	}
}

func main() {
	listen := flag.String("listen", "", "accept connections on this address (TCP Server mode)")
	connect := flag.String("connect", "", "connect to this address (TCP Client mode)")
	flag.Parse()
	sim := core.NewDemoSim()
	switch {
	case *listen != "":
		ln, err := net.Listen("tcp", *listen)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("fake device server listening on %s", ln.Addr())
		for {
			c, err := ln.Accept()
			if err != nil {
				log.Fatal(err)
			}
			go bridge(c, sim)
		}
	case *connect != "":
		for {
			c, err := net.Dial("tcp", *connect)
			if err != nil {
				time.Sleep(time.Second)
				continue
			}
			bridge(c, sim)
		}
	default:
		flag.Usage()
	}
}
