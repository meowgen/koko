package mysqlProxy

import (
	"context"
	proxy2 "github.com/meowgen/koko/pkg/go-mysql-proxy/proxy"
	"github.com/meowgen/koko/pkg/jms-sdk-go/service"
	"log"
	"os"
	"os/signal"
)

type Server struct {
}

func Start(jmsService *service.JMService) {
	ctx, cancel := context.WithCancel(context.Background())
	//todo: host/port
	proxy := proxy2.NewProxy("192.168.0.12", ":3306", ctx, jmsService)
	proxy.EnableDecoding()
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		for sig := range c {
			log.Printf("Signal received %v, stopping and exiting...", sig)
			cancel()
		}
	}()

	err := proxy.Start("3336")
	if err != nil {
		log.Fatal(err)
	}
}
