package psqlProxy

import (
	"fmt"
	pg3 "github.com/jackc/pgx/v5/pgproto3"
	"github.com/meowgen/koko/pkg/jms-sdk-go/service"
	"github.com/xdg/scram"
	"github.com/xdg/stringprep"
	"math/rand"
	"net"
	"reflect"
)

const (
	NoSSLConnection = "N"
	SSLConnection   = "S"
)

type BackendConnection struct {
	backend        *pg3.Backend
	conn           net.Conn
	startupMessage pg3.FrontendMessage
	server         *scram.Server
	servConv       *scram.ServerConversation
	Token          *service.TokenAuthInfoResponse
}

func NewBackendConnection(conn net.Conn, jmsService service.JMService) (*BackendConnection, error) {
	backend := pg3.NewBackend(conn, conn)
	token, callback, err := genServerCallback(jmsService)
	if err != nil {
		return nil, err
	}

	serverSHA256, err := scram.SHA256.NewServer(callback)
	connHandler := &BackendConnection{
		backend:  backend,
		conn:     conn,
		server:   serverSHA256,
		servConv: serverSHA256.NewConversation(),
		Token:    token,
	}

	return connHandler, nil
}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

func randStringRunes(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

func genServerCallback(jmsService service.JMService) (*service.TokenAuthInfoResponse, scram.CredentialLookup, error) {
	hgf := scram.SHA256
	kf := scram.KeyFactors{Iters: 4096}
	var client *scram.Client
	var tokenAuthInfo service.TokenAuthInfoResponse
	cbFcn := func(token string) (scram.StoredCredentials, error) {
		tokenAuth, err := jmsService.GetConnectTokenAuth(token)

		if err != nil || tokenAuth.Err != nil {
			return scram.StoredCredentials{}, fmt.Errorf("invalid token")
		}

		tokenAuthInfo.Info = tokenAuth.Info
		tokenAuthInfo.Err = tokenAuth.Err

		saltByte := make([]byte, 8)
		rand.Read(saltByte)
		salt1 := []byte(randStringRunes(8))

		salt2Byte := make([]byte, 12)
		rand.Read(salt2Byte)
		salt2 := []byte(randStringRunes(12))

		password := tokenAuthInfo.Info.Secret // password
		saltFull := append(salt1, salt2...)   // salt

		kf.Salt = string(saltFull)

		client, err = hgf.NewClient(token, password, "")
		if err != nil {
			return scram.StoredCredentials{}, fmt.Errorf("error generating client for credential callback: %v", err)
		}

		return client.GetStoredCredentials(kf), nil
	}
	return &tokenAuthInfo, cbFcn, nil
}

func (backConn *BackendConnection) AuthConnect() error {
	err := backConn.ReceiveSSLRequest()
	if err != nil {
		return err
	}

	err = backConn.ReceiveStartupMessage()
	if err != nil {
		return err
	}

	err = backConn.SendAuthSASLContinue()
	if err != nil {
		return err
	}

	err = backConn.SendAuthSASLFinal()
	if err != nil {
		return err
	}

	err = backConn.SendAuthOk()
	if err != nil {
		return err
	}

	return nil
}

func (backConn *BackendConnection) ReceiveSSLRequest() error {
	sslrequestMessage, err := backConn.backend.ReceiveStartupMessage()
	if err != nil {
		return fmt.Errorf("error receiving sslrequest: %w", err)
	}

	if reflect.TypeOf(sslrequestMessage).String() != reflect.TypeOf(&(pg3.SSLRequest{})).String() {
		return fmt.Errorf("received msg is not sslrequest: %w", err)
	}

	_, err = backConn.conn.Write([]byte(NoSSLConnection))
	return err
}

func (backConn *BackendConnection) ReceiveStartupMessage() error {
	startupMessage, err := backConn.backend.ReceiveStartupMessage()
	if err != nil {
		return fmt.Errorf("error receiving startup message: %w", err)
	}
	backConn.startupMessage = startupMessage

	if reflect.TypeOf(startupMessage).String() != reflect.TypeOf(&(pg3.StartupMessage{})).String() {
		return fmt.Errorf("received msg is not startup: %w", err)
	}

	msg := &pg3.AuthenticationSASL{AuthMechanisms: []string{"SCRAM-SHA-256"}}
	backConn.backend.Send(msg)
	return backConn.backend.Flush()
}

func (backConn *BackendConnection) SendAuthSASLContinue() error {
	backConn.backend.SetAuthType(pg3.AuthTypeSASL)
	message, err := backConn.backend.Receive()
	if err != nil {
		return fmt.Errorf("error receiving message: %w", err)
	}

	if reflect.TypeOf(message).String() != reflect.TypeOf(&(pg3.SASLInitialResponse{})).String() {
		return fmt.Errorf("Received message is not SASLInitialResponse: %w", err)
	}
	username := backConn.startupMessage.(*pg3.StartupMessage).Parameters["user"]
	if username, err = stringprep.SASLprep.Prepare(username); err != nil {
		return fmt.Errorf("Error SASLprepping username '%s': %v", username, err)
	}

	servfirstmsg, err := backConn.servConv.Step(string(message.(*pg3.SASLInitialResponse).Data), username)
	if err != nil {
		return err
	}

	authSASLContinue := &pg3.AuthenticationSASLContinue{Data: []byte(servfirstmsg)}
	backConn.backend.Send(authSASLContinue)
	return backConn.backend.Flush()
}

func (backConn *BackendConnection) SendAuthSASLFinal() error {
	backConn.backend.SetAuthType(pg3.AuthTypeSASLContinue)
	message, err := backConn.backend.Receive()
	if err != nil {
		return fmt.Errorf("error receiving message: %w", err)
	}

	if reflect.TypeOf(message).String() != reflect.TypeOf(&(pg3.SASLResponse{})).String() {
		return fmt.Errorf("Received message is not SASLResponse: %w", err)
	}
	servfinalmsg, err := backConn.servConv.Step(string(message.(*pg3.SASLResponse).Data), "")
	if err != nil {
		return err
	}

	authSASLFinal := &pg3.AuthenticationSASLFinal{Data: []byte(servfinalmsg)}
	backConn.backend.Send(authSASLFinal)
	backConn.backend.SetAuthType(pg3.AuthTypeSASLFinal)
	return backConn.backend.Flush()
}

func (backConn *BackendConnection) SendAuthOk() error {
	backConn.backend.Send(&pg3.AuthenticationOk{})
	backConn.backend.SetAuthType(pg3.AuthTypeOk)
	return backConn.backend.Flush()
}

func (backConn *BackendConnection) Close() error {
	return backConn.conn.Close()
}

func (backConn *BackendConnection) SendErrorResponse() {
	msg := fmt.Sprintf("password authentication failed for \"%s\"", backConn.startupMessage.(*pg3.StartupMessage).Parameters["username"])
	errorresponse := pg3.ErrorResponse{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "28P01", Message: msg}
	backConn.backend.Send(&errorresponse)
	backConn.backend.Flush()
}
