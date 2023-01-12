package psqlProxy

import (
	"context"
	"fmt"
	"github.com/meowgen/koko/pkg/common"
	modelCommon "github.com/meowgen/koko/pkg/jms-sdk-go/common"
	"github.com/meowgen/koko/pkg/jms-sdk-go/model"
	"github.com/meowgen/koko/pkg/jms-sdk-go/service"
	"github.com/meowgen/koko/pkg/proxy"
	"net"
	"time"
)

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

func (prs *ProxyServer) createNewSession(token service.TokenAuthInfoResponse, conn net.Conn) *CurrSession {
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
	CurrSession := &CurrSession{
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

	return CurrSession
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

func (proxyConn *ProxyConnection) closeCurrentSession() {
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
	proxyConn.backendConnection.conn.Close()
}
