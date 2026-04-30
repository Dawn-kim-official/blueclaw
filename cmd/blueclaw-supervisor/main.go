package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"blueclaw/internal/config"
	"blueclaw/internal/firecracker"
)

func main() {
	runtimeConfigurationPath := flag.String("runtime", "config/runtime.example.yaml", "runtime configuration path")
	flag.Parse()

	runtimeConfiguration, errorValue := config.LoadRuntimeConfiguration(*runtimeConfigurationPath)
	if errorValue != nil {
		log.Fatal(errorValue)
	}

	interruptContext, stopSignalNotification := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignalNotification()

	supervisorService := firecracker.NewSupervisorService(
		runtimeConfiguration.Firecracker,
		firecracker.WorkspaceVolumeService{},
		firecracker.VSockGuestHealthClient{},
	)

	guestInstance, errorValue := supervisorService.BootGuest(interruptContext)
	if errorValue != nil {
		log.Fatal(errorValue)
	}

	listenerContext, stopListenerProxies := context.WithCancel(interruptContext)
	defer stopListenerProxies()
	for _, listenerProxyConfiguration := range runtimeConfiguration.Firecracker.GuestListenerProxies {
		listenerProxyConfiguration := listenerProxyConfiguration
		go func() {
			errorValue := firecracker.GuestListenerProxy{
				VSockUnixSocketPath:  guestInstance.BootSpecification.VSockUnixSocketPath,
				GuestPort:            listenerProxyConfiguration.GuestPort,
				TargetUnixSocketPath: listenerProxyConfiguration.TargetUnixSocketPath,
			}.Serve(listenerContext)
			if errorValue != nil && listenerContext.Err() == nil {
				log.Printf("guest listener proxy stopped: %v", errorValue)
			}
		}()
	}

	healthContext, cancelHealthCheck := context.WithTimeout(interruptContext, 120*time.Second)
	defer cancelHealthCheck()

	errorValue = supervisorService.WaitForGuestHealth(healthContext, guestInstance)
	if errorValue != nil {
		_ = supervisorService.StopGuest(guestInstance)
		log.Fatal(errorValue)
	}

	proxyContext, stopProxy := context.WithCancel(interruptContext)
	defer stopProxy()
	proxyErrorChannel := make(chan error, 1)
	go func() {
		proxyErrorChannel <- firecracker.HostHTTPProxy{
			ListenAddress:       runtimeConfiguration.Firecracker.HostHTTPListenAddress,
			VSockUnixSocketPath: guestInstance.BootSpecification.VSockUnixSocketPath,
			GuestPortOrService:  runtimeConfiguration.Firecracker.GuestHTTPPortOrService,
		}.Serve(proxyContext)
	}()

	select {
	case errorValue = <-proxyErrorChannel:
		_ = supervisorService.StopGuest(guestInstance)
		log.Fatal(errorValue)
	case <-time.After(200 * time.Millisecond):
	}

	<-interruptContext.Done()
	stopProxy()
	stopListenerProxies()
	errorValue = supervisorService.StopGuest(guestInstance)
	if errorValue != nil {
		log.Fatal(errorValue)
	}
}
