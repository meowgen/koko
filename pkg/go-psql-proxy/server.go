package psqlProxy

import (
	"encoding/base64"
	"fmt"
	pg3 "github.com/jackc/pgx/v5/pgproto3"
	"github.com/meowgen/koko/pkg/jms-sdk-go/model"
	"github.com/meowgen/koko/pkg/proxy"
	"github.com/xdg/scram"
	"github.com/xdg/stringprep"
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
	realuser       *RealUser
	CurrSession    *CurrSession
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

type RealUser struct {
	username string
	password string
	database string
}

func (fs BackendConnection) NewBackendConnection(conn net.Conn) (*BackendConnection, error) {
	defer func() {
		if fs.CurrSession != nil {
			if fs.CurrSession.cmdRecorder != nil && fs.CurrSession.replRecorder != nil {
				fs.CurrSession.cmdRecorder.End()
				fs.CurrSession.replRecorder.End()
			}
			err := fs.CurrSession.DisConnectedCallback()
			if err != nil {
				fmt.Printf("error on disconnecting session")
			}
		}
		fs.conn.Close()
	}()
	fs.conn = conn
	backend := pg3.NewBackend(conn, conn)
	realuser, callback, err := genServerCallback()
	if err != nil {
		return nil, err
	}

	serverSHA256, err := scram.SHA256.NewServer(callback)
	connHandler := &BackendConnection{
		backend:  backend,
		conn:     conn,
		server:   serverSHA256,
		servConv: serverSHA256.NewConversation(),
		realuser: realuser,
	}

	return connHandler, nil
}

func genServerCallback() (*RealUser, scram.CredentialLookup, error) {
	hgf := scram.SHA256
	kf := scram.KeyFactors{Iters: 4096}
	var client *scram.Client
	var realuser RealUser
	cbFcn := func(token string) (scram.StoredCredentials, error) {
		tokenLogin := "6b73919f-2890-4ee0-8807-984e09e54d32" //TODO get token from another func

		if token != tokenLogin {
			return scram.StoredCredentials{}, fmt.Errorf("invalid token")
		}

		if token == tokenLogin {
			// 2. if equal get data else return error
			salt := "6PhmlKaHxlZIQToV0f9k3A==" // TODO from func
			password := "pESC8QdfLje85VNk5"    // TODO from func
			realuser.username = "baeldung"     // TODO from func
			realuser.password = "baeldung"     // TODO from func
			realuser.database = "baeldung"     // TODO from func

			saltBytes, err := base64.StdEncoding.DecodeString(salt)
			if err != nil {
				return scram.StoredCredentials{}, fmt.Errorf("error decoding salt: %v", err)
			}
			kf.Salt = string(saltBytes)

			client, err = hgf.NewClient(tokenLogin, password, "")
			if err != nil {
				return scram.StoredCredentials{}, fmt.Errorf("error generating client for credential callback: %v", err)
			}

			if tokenLogin, err = stringprep.SASLprep.Prepare(tokenLogin); err != nil {
				return scram.StoredCredentials{}, fmt.Errorf("Error SASLprepping username '%s': %v", "testuser", err)
			}

			return client.GetStoredCredentials(kf), nil
		} else {
			return scram.StoredCredentials{}, fmt.Errorf("Unknown token: %s", token)
		}
	}

	return &realuser, cbFcn, nil
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
	msg := fmt.Sprintf("password authentication failed for user")
	errorresponse := pg3.ErrorResponse{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "28P01", Message: msg}
	backConn.backend.Send(&errorresponse)
	backConn.backend.Flush()
}
