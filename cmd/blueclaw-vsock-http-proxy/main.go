//go:build linux

package main

import (
	"flag"
	"io"
	"log"
	"net"

	"github.com/mdlayher/vsock"
)

func main() {
	mode := flag.String("mode", "tcp-to-vsock", "proxy mode: tcp-to-vsock or vsock-to-tcp")
	listenAddress := flag.String("listen", "127.0.0.1:18081", "TCP listen address")
	listenVSockPort := flag.Uint("listen-vsock-port", 8081, "guest vsock listen port")
	targetTCPAddress := flag.String("target-tcp", "127.0.0.1:8080", "target TCP address")
	hostCID := flag.Uint("host-cid", 2, "host vsock CID")
	hostPort := flag.Uint("host-port", 7000, "host vsock port")
	flag.Parse()

	if *mode == "vsock-to-tcp" {
		if errorValue := serveVSockToTCP(uint32(*listenVSockPort), *targetTCPAddress); errorValue != nil {
			log.Fatal(errorValue)
		}
		return
	}

	listener, errorValue := net.Listen("tcp", *listenAddress)
	if errorValue != nil {
		log.Fatal(errorValue)
	}
	defer listener.Close()

	for {
		connection, errorValue := listener.Accept()
		if errorValue != nil {
			log.Printf("accept proxy connection: %v", errorValue)
			continue
		}
		go proxyConnection(connection, uint32(*hostCID), uint32(*hostPort))
	}
}

func serveVSockToTCP(listenPort uint32, targetTCPAddress string) error {
	listener, errorValue := listenVSock(listenPort)
	if errorValue != nil {
		return errorValue
	}
	defer listener.Close()

	for {
		connection, errorValue := listener.Accept()
		if errorValue != nil {
			log.Printf("accept vsock proxy connection: %v", errorValue)
			continue
		}
		go proxyVSockToTCP(connection, targetTCPAddress)
	}
}

func listenVSock(port uint32) (net.Listener, error) {
	return vsock.Listen(port, nil)
}

func proxyVSockToTCP(clientConnection net.Conn, targetTCPAddress string) {
	defer clientConnection.Close()

	targetConnection, errorValue := net.Dial("tcp", targetTCPAddress)
	if errorValue != nil {
		log.Printf("dial target tcp: %v", errorValue)
		return
	}
	defer targetConnection.Close()

	done := make(chan struct{}, 2)
	go copyAndClose(targetConnection, clientConnection, done)
	go copyAndClose(clientConnection, targetConnection, done)
	<-done
}

func proxyConnection(clientConnection net.Conn, hostCID uint32, hostPort uint32) {
	defer clientConnection.Close()

	hostConnection, errorValue := dialVSock(hostCID, hostPort)
	if errorValue != nil {
		log.Printf("dial host vsock: %v", errorValue)
		return
	}
	defer hostConnection.Close()

	done := make(chan struct{}, 2)
	go copyAndClose(hostConnection, clientConnection, done)
	go copyAndClose(clientConnection, hostConnection, done)
	<-done
}

func dialVSock(hostCID uint32, hostPort uint32) (net.Conn, error) {
	return vsock.Dial(hostCID, hostPort, nil)
}

func copyAndClose(destination io.WriteCloser, source io.Reader, done chan<- struct{}) {
	_, _ = io.Copy(destination, source)
	_ = destination.Close()
	done <- struct{}{}
}
