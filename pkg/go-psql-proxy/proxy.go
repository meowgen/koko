package psqlProxy

import (
	"context"
	"fmt"
	pg3 "github.com/jackc/pgx/v5/pgproto3"
	"github.com/meowgen/koko/pkg/common"
	modelCommon "github.com/meowgen/koko/pkg/jms-sdk-go/common"
	"github.com/meowgen/koko/pkg/jms-sdk-go/model"
	"github.com/meowgen/koko/pkg/jms-sdk-go/service"
	"github.com/meowgen/koko/pkg/logger"
	"github.com/meowgen/koko/pkg/proxy"
	"log"
	"net"
	"reflect"
	"time"
)

type ProxyServer struct {
	JmsService *service.JMService
}

type ProxyConnection struct {
	backendConnection  *BackendConnection  // as fake-server
	frontendConnection *FrontendConnection // as fake-client
	CurrSession        *CurrSession
}

type CurrSession struct {
	sess                     *model.Session
	replRecorder             *proxy.ReplyRecorder
	cmdRecorder              *proxy.CommandRecorder
	CreateSessionCallback    func() error
	ConnectedSuccessCallback func() error
	ConnectedFailedCallback  func(err error) error
	DisConnectedCallback     func() error
	SwSess                   *proxy.SwitchSession
}

func (prs *ProxyServer) Start() {
	ln, err := net.Listen("tcp", "192.168.0.34:5432")
	if err != nil {
		fmt.Println(err)
		return
	}

	for {
		clientConn, err := ln.Accept()
		if err != nil {
			fmt.Println(err)
			continue
		}

		go prs.NewHandleConnection(clientConn)
	}
}

func (prs *ProxyServer) NewHandleConnection(clientConn net.Conn) {
	backconn, err := NewBackendConnection(clientConn, *prs.JmsService)
	if err != nil {
		fmt.Println(err)
		return
	}
	err = backconn.AuthConnect()
	if err != nil {
		backconn.SendErrorResponse()
		fmt.Println(err)
		return
	}

	proxyConn := ProxyConnection{backendConnection: backconn}

	proxyConn.frontendConnection, err = NewFrontendConnection(*proxyConn.backendConnection.Token, "")
	if err != nil {
		fmt.Println(err)
		return
	}

	newStartupMessage := proxyConn.backendConnection.startupMessage.(*pg3.StartupMessage)
	newStartupMessage.Parameters["user"] = proxyConn.backendConnection.Token.Info.SystemUserAuthInfo.Username
	newStartupMessage.Parameters["database"] = proxyConn.backendConnection.Token.Info.Application.Attrs.Database
	err = proxyConn.frontendConnection.AuthConnect(*newStartupMessage)
	if err != nil {
		proxyConn.backendConnection.SendErrorResponse()
		fmt.Println(err)
		return
	}

	apiSession := &model.Session{
		ID:           common.UUID(),
		User:         proxyConn.backendConnection.Token.Info.User.String(),
		SystemUser:   proxyConn.backendConnection.Token.Info.SystemUserAuthInfo.String(),
		LoginFrom:    "DT",
		RemoteAddr:   proxyConn.backendConnection.conn.RemoteAddr().String(),
		Protocol:     proxyConn.backendConnection.Token.Info.SystemUserAuthInfo.Protocol,
		UserID:       proxyConn.backendConnection.Token.Info.User.ID,
		SystemUserID: proxyConn.backendConnection.Token.Info.SystemUserAuthInfo.ID,
		Asset:        proxyConn.backendConnection.Token.Info.Application.String(),
		AssetID:      proxyConn.backendConnection.Token.Info.Application.ID,
		OrgID:        proxyConn.backendConnection.Token.Info.Application.OrgID,
	}
	var ctx, cancel = context.WithCancel(context.Background())
	proxyConn.CurrSession = &CurrSession{
		sess: apiSession,
		CreateSessionCallback: func() error {
			apiSession.DateStart = modelCommon.NewNowUTCTime()
			return prs.JmsService.CreateSession(*apiSession)
		},
		ConnectedSuccessCallback: func() error {
			return prs.JmsService.SessionSuccess(apiSession.ID)
		},
		ConnectedFailedCallback: func(err error) error {
			return prs.JmsService.SessionFailed(apiSession.ID, err)
		},
		DisConnectedCallback: func() error {
			return prs.JmsService.SessionDisconnect(apiSession.ID)
		},
		SwSess: &proxy.SwitchSession{
			ID:            apiSession.ID,
			MaxIdleTime:   120,
			KeepAliveTime: 10000,
			Ctx:           ctx,
			Cancel:        cancel,
		},
	}
	proxy.AddCommonSwitch(proxyConn.CurrSession.SwSess)
	defer func() {
		if proxyConn.CurrSession != nil {
			if proxyConn.CurrSession != nil && proxyConn.CurrSession.replRecorder != nil {
				proxyConn.CurrSession.cmdRecorder.End()
				proxyConn.CurrSession.replRecorder.End()
			}
			err := proxyConn.CurrSession.DisConnectedCallback()
			if err != nil {
				fmt.Printf("error on disconnecting session: %s", err)
			}
		}
		clientConn.Close()
	}()

	if err := proxyConn.CurrSession.CreateSessionCallback(); err != nil {
		log.Printf("%s", err.Error())
		return
	}
	if err := proxyConn.CurrSession.ConnectedSuccessCallback(); err != nil {
		log.Printf("%s", err.Error())
		return
	}
	proxyConn.CurrSession.replRecorder, proxyConn.CurrSession.cmdRecorder = prs.GetRecorders(proxyConn)

	err = proxyConn.Run()
	if err != nil {
		log.Fatal(err)
	}
}

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

func (proxyConn *ProxyConnection) Run() error {
	defer func() {
		proxyConn.Close()
		proxyConn.CurrSession.cmdRecorder.End()
		proxyConn.CurrSession.replRecorder.End()
		proxyConn.CurrSession.DisConnectedCallback()
	}()

	frontendErrChan := make(chan error, 1)
	frontendMsgChan := make(chan pg3.FrontendMessage)
	frontendNextChan := make(chan struct{})
	go proxyConn.readClientConn(frontendMsgChan, frontendNextChan, frontendErrChan)

	backendErrChan := make(chan error, 1)
	backendMsgChan := make(chan pg3.BackendMessage)
	backendNextChan := make(chan struct{})
	go proxyConn.readServerConn(backendMsgChan, backendNextChan, backendErrChan)

	sessEndBySite := false
	for {
		select {
		case msg := <-frontendMsgChan:
			if sessEndBySite {
				proxyConn.backendConnection.backend.Send(&pg3.ErrorResponse{Message: "connection closed by site"})
				proxyConn.backendConnection.backend.Flush()
				return nil
			}

			proxyConn.frontendConnection.frontend.Send(msg)

			switch msg.(type) {
			case *pg3.Parse:
				proxyConn.RecordQuery(msg.(*pg3.Parse).Query)
			case *pg3.Query:
				proxyConn.RecordQuery(msg.(*pg3.Query).String)
				proxyConn.frontendConnection.frontend.Flush()
			case *pg3.Sync:
				proxyConn.frontendConnection.frontend.Flush()
			case *pg3.Terminate:
				proxyConn.frontendConnection.frontend.Flush()
				return nil
			}
			frontendNextChan <- struct{}{}

		case msg := <-backendMsgChan:
			proxyConn.backendConnection.backend.Send(msg)
			if reflect.TypeOf(msg).String() == reflect.TypeOf(&(pg3.ReadyForQuery{})).String() {
				proxyConn.backendConnection.backend.Flush()
			}
			backendNextChan <- struct{}{}

		case err := <-frontendErrChan:
			return err
		case err := <-backendErrChan:
			return err
		case <-proxyConn.CurrSession.SwSess.Ctx.Done():
			sessEndBySite = true
		}
	}
}

func (proxyConn *ProxyConnection) RecordQuery(query string) {
	cmd := &model.Command{
		SessionID:  proxyConn.CurrSession.sess.ID,
		OrgID:      proxyConn.CurrSession.sess.OrgID,
		Input:      query,
		Output:     "",
		User:       proxyConn.CurrSession.sess.User,
		Server:     proxyConn.CurrSession.sess.Asset,
		SystemUser: proxyConn.CurrSession.sess.SystemUser,
		Timestamp:  time.Now().Unix(),
		RiskLevel:  0,
	}
	proxyConn.CurrSession.cmdRecorder.RecordCommand(cmd)
	proxyConn.CurrSession.replRecorder.Record([]byte(query + "\r\n"))
}

func (proxyConn *ProxyConnection) readClientConn(msgChan chan pg3.FrontendMessage, nextChan chan struct{}, errChan chan error) {
	for {
		msg, err := proxyConn.backendConnection.backend.Receive()
		if err != nil {
			errChan <- err
			return
		}

		msgChan <- msg
		<-nextChan
	}
}

func (proxyConn *ProxyConnection) readServerConn(msgChan chan pg3.BackendMessage, nextChan chan struct{}, errChan chan error) {
	for {
		msg, err := proxyConn.frontendConnection.frontend.Receive()
		if err != nil {
			errChan <- err
			return
		}

		msgChan <- msg
		<-nextChan
	}
}

func (proxyConn *ProxyConnection) Close() error {
	frontendCloseErr := proxyConn.frontendConnection.conn.Close()
	backendCloseErr := proxyConn.backendConnection.conn.Close()

	if frontendCloseErr != nil {
		return frontendCloseErr
	}
	return backendCloseErr
}
