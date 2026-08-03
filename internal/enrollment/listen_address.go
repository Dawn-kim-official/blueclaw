package enrollment

import (
	"net"
	"strconv"
)

const firstListenPort = 8080

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
