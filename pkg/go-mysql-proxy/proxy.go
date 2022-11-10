package mysqlProxy

import (
	"context"
	"github.com/meowgen/koko/pkg/jms-sdk-go/service"
)

func NewProxy(host, port string, ctx context.Context, jmsService *service.JMService) *Proxy {
	return &Proxy{
		host:       host,
		port:       port,
		ctx:        ctx,
		jmsService: jmsService,
	}
}

type Proxy struct {
	jmsService     *service.JMService
	host           string
	port           string
	connectionId   uint64
	enableDecoding bool
	ctx            context.Context
	shutDownAsked  bool
}
