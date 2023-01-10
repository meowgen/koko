package psqlProxy

import (
	"fmt"
	pg3 "github.com/jackc/pgx/v5/pgproto3"
	"github.com/meowgen/koko/pkg/jms-sdk-go/service"
	"log"
	"net"
	"reflect"
)

type ProxyServer struct {
	backendConnection  *BackendConnection  // as fake-server
	frontendConnection *FrontendConnection // as fake-client
	JmsService         *service.JMService
	CurrSession        *CurrSession
	Token              *service.TokenAuthInfoResponse
}

func (prs *ProxyServer) Start() {
	ln, err := net.Listen("tcp", "192.168.0.34:5432")
	if err != nil {
		log.Fatal(err)
	}

	for {
		clientConn, err := ln.Accept()
		if err != nil {
			fmt.Println(err)
			continue
		}

		backconn := BackendConnection{}
		backconn.NewBackendConnection(clientConn)
		if err != nil {
			fmt.Println(err)
			continue
		}

		prx := ProxyServer{
			backendConnection: &backconn,
		}
		go prx.AcceptConnection()
	}
}

func (prs *ProxyServer) AcceptConnection() {
	err := prs.backendConnection.AuthConnect()
	if err != nil {
		prs.backendConnection.SendErrorResponse()
		fmt.Println(err)
		return
	}

	prs.frontendConnection, err = NewFrontendConnection(prs.backendConnection.realuser.username, prs.backendConnection.realuser.password, "") // TODO: backside server is down
	if err != nil {
		fmt.Println(err)
		return
	}
	newStartupMessage := prs.backendConnection.startupMessage.(*pg3.StartupMessage)
	newStartupMessage.Parameters["user"] = prs.backendConnection.realuser.username
	newStartupMessage.Parameters["database"] = prs.backendConnection.realuser.database
	err = prs.frontendConnection.AuthConnect(*newStartupMessage)
	if err != nil {
		prs.backendConnection.SendErrorResponse()
		fmt.Println(err)
		return
	}

	err = prs.Run()
	if err != nil {
		log.Fatal(err)
	}
}

func (prs *ProxyServer) Run() error {
	defer prs.Close()

	frontendErrChan := make(chan error, 1)
	frontendMsgChan := make(chan pg3.FrontendMessage)
	frontendNextChan := make(chan struct{})
	go prs.readClientConn(frontendMsgChan, frontendNextChan, frontendErrChan)

	backendErrChan := make(chan error, 1)
	backendMsgChan := make(chan pg3.BackendMessage)
	backendNextChan := make(chan struct{})
	go prs.readServerConn(backendMsgChan, backendNextChan, backendErrChan)

	for {
		select {
		case msg := <-frontendMsgChan:
			prs.frontendConnection.frontend.Send(msg)

			switch msg.(type) {
			case *pg3.Parse:
				fmt.Println(msg.(*pg3.Parse).Query)
			case *pg3.Query:
				fmt.Println(msg.(*pg3.Query))
				prs.frontendConnection.frontend.Flush()
			case *pg3.Sync:
				prs.frontendConnection.frontend.Flush()
			case *pg3.Terminate:
				prs.frontendConnection.frontend.Flush()
				return nil
			}
			frontendNextChan <- struct{}{}

		case msg := <-backendMsgChan:
			prs.backendConnection.backend.Send(msg)
			if reflect.TypeOf(msg).String() == reflect.TypeOf(&(pg3.ReadyForQuery{})).String() {
				prs.backendConnection.backend.Flush()
			}
			backendNextChan <- struct{}{}

		case err := <-frontendErrChan:
			return err
		case err := <-backendErrChan:
			return err
		}
	}
}

func (prs *ProxyServer) readClientConn(msgChan chan pg3.FrontendMessage, nextChan chan struct{}, errChan chan error) {
	for {
		msg, err := prs.backendConnection.backend.Receive()
		if err != nil {
			errChan <- err
			return
		}

		msgChan <- msg
		<-nextChan
	}
}

func (prs *ProxyServer) readServerConn(msgChan chan pg3.BackendMessage, nextChan chan struct{}, errChan chan error) {
	for {
		msg, err := prs.frontendConnection.frontend.Receive()
		if err != nil {
			errChan <- err
			return
		}

		msgChan <- msg
		<-nextChan
	}
}

func (prs *ProxyServer) Close() error {
	frontendCloseErr := prs.frontendConnection.conn.Close()
	backendCloseErr := prs.backendConnection.conn.Close()

	if frontendCloseErr != nil {
		return frontendCloseErr
	}
	return backendCloseErr
}
