package mysqlProxy

import (
	"bytes"
	"fmt"
	"github.com/meowgen/koko/pkg/jms-sdk-go/model"
	"github.com/meowgen/koko/pkg/jms-sdk-go/service"
	"github.com/meowgen/koko/pkg/logger"
	"github.com/meowgen/koko/pkg/proxy"
	"golang.org/x/text/encoding/charmap"
	"net"
	"time"
)

var mySqlPrompt = []byte("mysql>")

type Connection struct {
	conn       net.Conn
	host       string
	port       string
	JmsService *service.JMService
	FakeServer *FakeServer
	Finished   *chan struct{}
}

func NewConnection(fakeSrv *FakeServer) *Connection {
	return &Connection{
		host:       fakeSrv.Host,
		port:       fmt.Sprintf(":%d", fakeSrv.Port),
		conn:       fakeSrv.Conn,
		JmsService: fakeSrv.JmsService,
		FakeServer: fakeSrv,
	}
}

func (fs *FakeServer) GetRecorders() (*proxy.ReplyRecorder, *proxy.CommandRecorder) {
	return fs.GetReplayRecorder(), fs.GetCommandRecorder()
}

func (fs *FakeServer) GetReplayRecorder() *proxy.ReplyRecorder {
	info := &proxy.ReplyInfo{
		Width:     80,
		Height:    36,
		TimeStamp: time.Now(),
	}
	termConf, err := fs.JmsService.GetTerminalConfig()
	recorder, err := proxy.NewReplayRecord(fs.CurrSession.sess.ID, fs.JmsService,
		proxy.NewReplayStorage(fs.JmsService, &termConf),
		info)
	if err != nil {
		logger.Error(err)
	}
	return recorder
}

func (fs *FakeServer) GetCommandRecorder() *proxy.CommandRecorder {
	termConf, _ := fs.JmsService.GetTerminalConfig()
	cmdR := proxy.CommandRecorder{
		SessionID:  fs.CurrSession.sess.ID,
		Storage:    proxy.NewCommandStorage(fs.JmsService, &termConf),
		Queue:      make(chan *model.Command, 10),
		Closed:     make(chan struct{}),
		JmsService: fs.JmsService,
	}
	go cmdR.Record()
	return &cmdR
}

type Request struct {
	buf        bytes.Buffer
	jmsService *service.JMService
	currSess   *CurrSession
	fs         *FakeServer
}

type Response struct {
	buf        bytes.Buffer
	jmsService *service.JMService
	currSess   *CurrSession
	fs         *FakeServer
}

func beatify(b []byte) []byte {
	b = bytes.Trim(b, "\x00")
	b = append(b, []byte{'\r', '\n'}...)
	return b
}

func getPacketType(packet []byte) byte {
	return packet[4]
}

func (req *Request) Write(packet []byte) (n int, err error) {

	packetType := getPacketType(packet)

	switch packetType {
	case ComQuit:
		fmt.Printf("\nПроизошёл выход.\n")
		req.currSess.cmdRecorder.End()
		req.currSess.replRecorder.End()
		req.currSess.DisConnectedCallback()
		return len(packet), nil
	case ComStmtPrepare:
	case ComQuery:
		req.buf.Write(packet[7:])
	}

	//if packet[4] != 3 {
	//	req.buf.Write(packet[5:])
	//} else {
	//	req.buf.Write(packet[7:])
	//}

	decoder := charmap.CodePage866.NewDecoder()
	cmdBytes, err := decoder.Bytes(req.buf.Bytes())

	if len(cmdBytes) == 0 {
		return len(packet), nil
	}
	cmdBytes = beatify(cmdBytes)

	fmt.Printf("\nКоманда: %s", string(cmdBytes))

	cmd := &model.Command{
		SessionID:  req.currSess.sess.ID,
		OrgID:      req.currSess.sess.OrgID,
		Input:      string(cmdBytes),
		Output:     "",
		User:       req.currSess.sess.User,
		Server:     req.currSess.sess.Asset,
		SystemUser: req.currSess.sess.SystemUser,
		Timestamp:  time.Now().Unix(),
		RiskLevel:  0,
	}
	finalCmdBytes := append(mySqlPrompt, cmdBytes...)
	req.currSess.cmdRecorder.RecordCommand(cmd)
	req.currSess.replRecorder.Record(finalCmdBytes)

	req.buf.Reset()
	return len(packet), nil
}

func (res *Response) Write(packet []byte) (n int, err error) {

	res.buf.Write(packet[7:])
	//fmt.Printf("\npacket %s", packet)
	//
	//if packet[4] != 0xff {
	//	fmt.Printf("\nКоманда успешно выполнилась.")
	//	res.buf.Reset()
	//	return len(packet), nil
	//}
	//if len(packet) > 11 {
	//	fmt.Printf("\nОтвет от сервера: %v\n", &res.buf)
	//	res.buf.Reset()
	//}

	return len(packet), nil
}
