package psqlProxy

import (
	"fmt"
	pg3 "github.com/jackc/pgx/v5/pgproto3"
	"github.com/meowgen/koko/pkg/jms-sdk-go/service"
	"github.com/meowgen/koko/pkg/proxy"
	"net"
	"reflect"
)

type ProxyServer struct {
	JmsService *service.JMService
}

type ProxyConnection struct {
	backendConnection  *BackendConnection  // as fake-server
	frontendConnection *FrontendConnection // as fake-client
	CurrSession        *CurrSession
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
		WriteErrorResponse(clientConn, "proxy server", "user or password authentication failed")
		return
	}
	if !backconn.Connecting() {
		WriteErrorResponse(clientConn, "proxy error", "user or password authentication failed")
		return
	}

	proxyConn := ProxyConnection{backendConnection: backconn}

	proxyConn.frontendConnection, err = NewFrontendConnection(*proxyConn.backendConnection.Token, "")
	if err != nil {
		WriteErrorResponse(clientConn, "proxy error", "user or password authentication failed")
		return
	}

	newStartupMessage := proxyConn.backendConnection.startupMessage.(*pg3.StartupMessage)
	newStartupMessage.Parameters["user"] = proxyConn.backendConnection.Token.Info.SystemUserAuthInfo.Username
	newStartupMessage.Parameters["database"] = proxyConn.backendConnection.Token.Info.Application.Attrs.Database
	if !proxyConn.frontendConnection.Connected(*newStartupMessage) {
		WriteErrorResponse(clientConn, "proxy error", "user or password authentication failed")
		return
	}

	currSession := prs.createNewSession(*proxyConn.backendConnection.Token, proxyConn.backendConnection.conn)

	proxyConn.CurrSession = currSession
	proxy.AddCommonSwitch(proxyConn.CurrSession.SwSess)
	defer proxyConn.closeCurrentSession()

	if err := proxyConn.CurrSession.CreateSessionCallback(); err != nil {
		WriteErrorResponse(clientConn, "proxy error", "user or password authentication failed")
		return
	}
	if err := proxyConn.CurrSession.ConnectedSuccessCallback(); err != nil {
		WriteErrorResponse(clientConn, "proxy error", "user or password authentication failed")
		return
	}
	proxyConn.CurrSession.replRecorder, proxyConn.CurrSession.cmdRecorder = prs.GetRecorders(proxyConn)

	proxyConn.Run()
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
				proxyConn.backendConnection.backend.Send(&pg3.ErrorResponse{Message: "Connection closed by administrator"})
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

func WriteErrorResponse(conn net.Conn, code, message string) {
	conn.Write((&pg3.ErrorResponse{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: code, Message: message}).Encode(nil))
}
