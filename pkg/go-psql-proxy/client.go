package psqlProxy

import (
	"fmt"
	pg3 "github.com/jackc/pgx/v5/pgproto3"
	"github.com/meowgen/koko/pkg/jms-sdk-go/service"
	"github.com/xdg/scram"
	"net"
)

type FrontendConnection struct {
	frontend   *pg3.Frontend
	conn       net.Conn
	clientConv *scram.ClientConversation
}

func NewFrontendConnection(token service.TokenAuthInfoResponse, authzID string) (*FrontendConnection, error) {
	address := fmt.Sprintf("%s:%d", token.Info.Application.Attrs.Host, token.Info.Application.Attrs.Port)
	dl, err := startdial("tcp", address)
	if err != nil {
		return nil, err
	}
	frontend := pg3.NewFrontend(dl, dl)
	connHandler := &FrontendConnection{
		frontend: frontend,
		conn:     dl,
	}
	cli, err := newclient(token.Info.SystemUserAuthInfo.Username, token.Info.SystemUserAuthInfo.Password, authzID)
	if err != nil {
		return nil, err
	}
	connHandler.clientConv = cli.NewConversation()

	return connHandler, nil
}

func startdial(network, address string) (net.Conn, error) {
	dl, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}
	return dl, nil
}

func newclient(username, password, authzID string) (scram.Client, error) {
	cl, err := scram.SHA256.NewClient(username, password, authzID)
	if err != nil {
		return scram.Client{}, err
	}

	return *cl, nil
}

func (frontConn *FrontendConnection) Connected(message pg3.StartupMessage) bool {
	err := frontConn.SendSSLRequest()
	if err != nil {
		return false
	}

	err = frontConn.SendStartupMessage(message)
	if err != nil {
		return false
	}

	servfirstmsg, err := frontConn.ReceiveServerFirstMessage()
	if err != nil {
		return false
	}

	servfinalmsg, err := frontConn.ReceiveServerFinalMessage(servfirstmsg)
	if err != nil {
		return false
	}

	_, err = frontConn.clientConv.Step(servfinalmsg)
	if err != nil {
		return false
	}

	err = frontConn.ReceiveAuthOk()
	if err != nil {
		return false
	}

	return true
}

func (frontConn *FrontendConnection) SendSSLRequest() error {
	frontConn.frontend.Send(&pg3.SSLRequest{})
	err := frontConn.frontend.Flush()
	if err != nil {
		return err
	}
	buffer := make([]byte, 1024)
	_, err = frontConn.conn.Read(buffer)
	return err
}

func (frontConn *FrontendConnection) SendStartupMessage(startupmsg pg3.StartupMessage) error {
	frontConn.frontend.Send(&startupmsg)
	return frontConn.frontend.Flush()
}

func (frontConn *FrontendConnection) ReceiveServerFirstMessage() (string, error) {
	authSASLmsg, err := frontConn.frontend.Receive()
	if err != nil {
		return "", err
	}

	clifirstmsg, err := frontConn.clientConv.Step("")
	if err != nil {
		return "", err
	}

	switch authSASLmsg.(type) {
	case *pg3.AuthenticationSASL:
		authSASLInit := &pg3.SASLInitialResponse{AuthMechanism: "SCRAM-SHA-256", Data: []byte(clifirstmsg)}
		frontConn.frontend.Send(authSASLInit)
		err = frontConn.frontend.Flush()
		if err != nil {
			return "", err
		}

		authSASLContinue, err := frontConn.frontend.Receive()
		if err != nil {
			return "", err
		}

		switch authSASLContinue.(type) {
		case *pg3.AuthenticationSASLContinue:
			data := authSASLContinue.(*pg3.AuthenticationSASLContinue).Data
			return string(data), nil
		case *pg3.ErrorResponse:
			return "", fmt.Errorf(authSASLContinue.(*pg3.ErrorResponse).Message)
		default:
			return "", fmt.Errorf("ReceiveServerFirstMessage switch second default")
		}
	case *pg3.ErrorResponse:
		return "", fmt.Errorf(authSASLmsg.(*pg3.ErrorResponse).Message)
	default:
		return "", fmt.Errorf("ReceiveServerFirstMessage switch default")
	}
}

func (frontConn *FrontendConnection) ReceiveServerFinalMessage(servfirstmsg string) (string, error) {
	clifinalmsg, err := frontConn.clientConv.Step(servfirstmsg)
	if err != nil {
		return "", err
	}

	saslresponsemsg := &pg3.SASLResponse{Data: []byte(clifinalmsg)}
	frontConn.frontend.Send(saslresponsemsg)
	err = frontConn.frontend.Flush()
	if err != nil {
		return "", err
	}

	servfinalmsg, err := frontConn.frontend.Receive()
	if err != nil {
		return "", err
	}

	switch servfinalmsg.(type) {
	case *pg3.AuthenticationSASLFinal:
		data := servfinalmsg.(*pg3.AuthenticationSASLFinal).Data
		return string(data), nil

	case *pg3.ErrorResponse:
		return "", fmt.Errorf(servfinalmsg.(*pg3.ErrorResponse).Message)

	default:
		return "", fmt.Errorf("ReceiveServerFinalMessage switch default")
	}
}

func (frontConn *FrontendConnection) ReceiveAuthOk() error {
	authok, err := frontConn.frontend.Receive()
	if err != nil {
		return err
	}
	switch authok.(type) {
	case *pg3.AuthenticationOk:
		return nil

	case *pg3.ErrorResponse:
		return fmt.Errorf(authok.(*pg3.ErrorResponse).Message)

	default:
		return fmt.Errorf("ReceiveAuthOk switch second default")
	}
}
