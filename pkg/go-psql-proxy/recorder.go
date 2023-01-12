package psqlProxy

import (
	"github.com/meowgen/koko/pkg/jms-sdk-go/model"
	"github.com/meowgen/koko/pkg/logger"
	"github.com/meowgen/koko/pkg/proxy"
	"time"
)

func (prs *ProxyServer) GetRecorders(proxyConn ProxyConnection) (*proxy.ReplyRecorder, *proxy.CommandRecorder) {
	return prs.GetReplayRecorder(proxyConn), prs.GetCommandRecorder(proxyConn)
}

func (prs *ProxyServer) GetReplayRecorder(proxyConn ProxyConnection) *proxy.ReplyRecorder {
	info := &proxy.ReplyInfo{
		Width:     80,
		Height:    36,
		TimeStamp: time.Now(),
	}
	termConf, err := prs.JmsService.GetTerminalConfig()
	recorder, err := proxy.NewReplayRecord(proxyConn.CurrSession.sess.ID, prs.JmsService,
		proxy.NewReplayStorage(prs.JmsService, &termConf),
		info)
	if err != nil {
		logger.Error(err)
	}
	return recorder
}

func (prs *ProxyServer) GetCommandRecorder(proxyConn ProxyConnection) *proxy.CommandRecorder {
	termConf, _ := prs.JmsService.GetTerminalConfig()
	cmdR := proxy.CommandRecorder{
		SessionID:  proxyConn.CurrSession.sess.ID,
		Storage:    proxy.NewCommandStorage(prs.JmsService, &termConf),
		Queue:      make(chan *model.Command, 10),
		Closed:     make(chan struct{}),
		JmsService: prs.JmsService,
	}
	go cmdR.Record()
	return &cmdR
}
