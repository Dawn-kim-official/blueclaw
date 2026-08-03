package enrollment

import (
	"net"
	"strconv"
)

const firstListenPort = 8080

const firstManagedPort = 25432

func availableManagedPort() uint32 {
	for candidatePort := firstManagedPort; candidatePort < firstManagedPort+256; candidatePort++ {
		listener, errorValue := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(candidatePort))
		if errorValue != nil {
			continue
		}
		listener.Close()
		return uint32(candidatePort)
	}
	return firstManagedPort
}

func availableListenAddress() string {
	for candidatePort := firstListenPort; candidatePort < firstListenPort+64; candidatePort++ {
		address := "127.0.0.1:" + strconv.Itoa(candidatePort)
		listener, errorValue := net.Listen("tcp", address)
		if errorValue != nil {
			continue
		}
		listener.Close()
		return address
	}
	return "127.0.0.1:" + strconv.Itoa(firstListenPort)
}
