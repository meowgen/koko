package mysqlProxy

import (
	"bytes"
	"context"
	"fmt"
	"github.com/meowgen/koko/pkg/common"
	modelCommon "github.com/meowgen/koko/pkg/jms-sdk-go/common"
	"github.com/meowgen/koko/pkg/jms-sdk-go/model"
	"github.com/meowgen/koko/pkg/jms-sdk-go/service"
	"github.com/meowgen/koko/pkg/proxy"
	"io"
	"log"
	"net"
)

type FakeServer struct {
	Conn        net.Conn
	JmsService  *service.JMService
	Host        string
	Port        int
	Username    string
	Password    string
	CurrSession *CurrSession
	Token       *service.TokenAuthInfoResponse
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

func (fs *FakeServer) Start() {
	listener, _ := net.Listen("tcp", "192.168.0.73:8888") // открываем слушающий сокет
	for {
		conn, err := listener.Accept() // принимаем TCP-соединение от клиента и создаем новый сокет
		if err != nil {
			continue
		}
		go fs.handleClient(conn) // обрабатываем запросы клиента в отдельной го-рутине
	}
}

func (fs *FakeServer) handleClient(conn net.Conn) {
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
		fs.Conn.Close()
	}()
	fs.Conn = conn

	fakeHandshakePacket := &FakeHandshakePacket{}

	_ = fakeHandshakePacket.NewHandshakePacket()
	packet, err := fakeHandshakePacket.Encode()
	if err != nil {
		fmt.Printf("%s", err)
		return
	}
	go conn.Write(packet)

	authPacket := GetAuthPacket(conn)
	username, password := authPacket.Username, authPacket.Password
	username = bytes.Trim(username, "\x00")
	token, err := fs.JmsService.GetConnectTokenAuth(string(username))
	fs.Token = &token

	if err != nil {
		fmt.Printf("invalid token")
		return
	}

	checkConnectPermFunc := func() (model.ExpireInfo, error) {
		return fs.JmsService.ValidateApplicationPermission(fs.Token.Info.User.ID,
			fs.Token.Info.Application.ID, fs.Token.Info.SystemUserAuthInfo.ID)
	}
	perm, _ := checkConnectPermFunc()
	if !perm.HasPermission {
		fmt.Printf("no perm")
		return
	}
	salt := append(fakeHandshakePacket.Salt, fakeHandshakePacket.Salt2...)

	tokenPass := token.Info.Secret
	scrambledTokenPass := ScramblePassword(salt, tokenPass)

	// соль токен-пароль пароль
	//fmt.Printf("\nsalt : %v tokenp : %s p : %s", salt, hex.EncodeToString(scrambledTokenPass), hex.EncodeToString(password))

	if !bytes.Equal(scrambledTokenPass, password) {
		fmt.Printf("wrong pass")
		return
	}

	fs.Host = token.Info.Application.Attrs.Host
	fs.Port = token.Info.Application.Attrs.Port

	fs.Username = token.Info.SystemUserAuthInfo.Username
	fs.Password = token.Info.SystemUserAuthInfo.Password
	apiSession := &model.Session{
		ID:           common.UUID(),
		User:         token.Info.User.String(),
		SystemUser:   token.Info.SystemUserAuthInfo.String(),
		LoginFrom:    "DT",
		RemoteAddr:   conn.RemoteAddr().String(),
		Protocol:     token.Info.SystemUserAuthInfo.Protocol,
		UserID:       token.Info.User.ID,
		SystemUserID: token.Info.SystemUserAuthInfo.ID,
		Asset:        token.Info.Application.String(),
		AssetID:      token.Info.Application.ID,
		OrgID:        token.Info.Application.OrgID,
	}
	var ctx, cancel = context.WithCancel(context.Background())
	fs.CurrSession = &CurrSession{
		sess: apiSession,
		CreateSessionCallback: func() error {
			apiSession.DateStart = modelCommon.NewNowUTCTime()
			return fs.JmsService.CreateSession(*apiSession)
		},
		ConnectedSuccessCallback: func() error {
			return fs.JmsService.SessionSuccess(apiSession.ID)
		},
		ConnectedFailedCallback: func(err error) error {
			return fs.JmsService.SessionFailed(apiSession.ID, err)
		},
		DisConnectedCallback: func() error {
			return fs.JmsService.SessionDisconnect(apiSession.ID)
		},
		SwSess: &proxy.SwitchSession{
			ID:            apiSession.ID,
			MaxIdleTime:   120,
			KeepAliveTime: 10000,
			Ctx:           ctx,
			Cancel:        cancel,
		},
	}

	proxy.AddCommonSwitch(fs.CurrSession.SwSess)

	authPacket.Username = []byte(fs.Username)
	authPacket.Password = []byte(fs.Password)
	connection := NewConnection(fs)

	address := fmt.Sprintf("%s%s", connection.host, connection.port)
	mysql, err := net.Dial("tcp", address)
	if err != nil {
		log.Printf("Failed to connection to MySQL: %s", err.Error())
		return
	}

	handshakePacket := &InitialHandshakePacket{}
	err = handshakePacket.Decode(mysql)

	if err != nil {
		log.Printf("Failed ot decode handshake initial packet: %s", err.Error())
		return
	}

	saltData := handshakePacket.AuthPluginData[:len(handshakePacket.AuthPluginData)-1]

	passwd := ScramblePassword(saltData, string(authPacket.Password))
	authPacket.Password = passwd

	res, err := authPacket.Encode()
	if err != nil {
		log.Printf("%s", err.Error())
		return
	}

	_, err = mysql.Write(res)
	if err != nil {
		log.Printf("%s", err.Error())
		return
	}

	if err := fs.CurrSession.CreateSessionCallback(); err != nil {
		log.Printf("%s", err.Error())
		return
	}
	if err := fs.CurrSession.ConnectedSuccessCallback(); err != nil {
		log.Printf("%s", err.Error())
		return
	}
	fs.CurrSession.replRecorder, fs.CurrSession.cmdRecorder = fs.GetRecorders()

	var request bytes.Buffer
	// Copy bytes from client to server and requestParser
	go io.Copy(io.MultiWriter(mysql, &Request{request, connection.JmsService, fs.CurrSession,
		connection.FakeServer}), connection.conn)

	// Copy bytes from server to client and responseParser
	var response bytes.Buffer
	go io.Copy(io.MultiWriter(connection.conn, &Response{response, connection.JmsService, fs.CurrSession,
		connection.FakeServer}), mysql)

	for {
		select {
		case <-fs.CurrSession.SwSess.Ctx.Done():
			return
		default:
			continue
		}
	}

}

func GetAuthPacket(conn net.Conn) *AuthorizationPacket {
	authorizationPacket := &AuthorizationPacket{}
	err := authorizationPacket.Decode(conn)
	if err != nil {
		fmt.Println(err)
		GetAuthPacket(conn)
	}
	return authorizationPacket
}
