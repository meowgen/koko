package srvconn

import (
	"fmt"
	_ "github.com/godror/godror"
	"github.com/meowgen/koko/pkg/localcommand"
	"os"
)

const (
	OraclePrompt = "Password for user %s:"
)

func NewOracleConnection(ops ...SqlOption) (*OracleConn, error) {
	args := &sqlOption{
		Username: os.Getenv("USER"),
		Password: os.Getenv("PASSWORD"),
		Host:     "127.0.0.1",
		Port:     1521,
		DBName:   "XEPDB1",
		win: Windows{
			Width:  80,
			Height: 120,
		},
	}
	for _, setter := range ops {
		setter(args)
	}
	//if err := checkOracleAccount(args); err != nil {
	//	return nil, err
	//}
	lCmd, err := startOracleCommand(args)
	if err != nil {
		return nil, err
	}
	err = lCmd.SetWinSize(args.win.Width, args.win.Height)
	if err != nil {
		_ = lCmd.Close()
		return nil, err
	}
	return &OracleConn{options: args, LocalCommand: lCmd}, nil
}

type OracleConn struct {
	options *sqlOption
	*localcommand.LocalCommand
}

func (conn *OracleConn) KeepAlive() error {
	return nil
}

func (conn *OracleConn) Close() error {
	_, _ = conn.Write([]byte("\r\nexit\r\n"))
	return conn.LocalCommand.Close()
}

func startOracleCommand(opt *sqlOption) (lcmd *localcommand.LocalCommand, err error) {
	argv := []string{fmt.Sprintf("%s/%s@//%s:%d/%s", opt.Username, opt.Password, opt.Host, opt.Port, opt.DBName)}
	lcmd, err = localcommand.New("sqlplus", argv, localcommand.WithPtyWin(opt.win.Width, opt.win.Height))
	if err != nil {
		return nil, err
	}
	//if opt.Password != "" {
	//	lcmd, err = MatchLoginPrefix(fmt.Sprintf(OraclePrompt, opt.Username), "Oracle", lcmd)
	//	if err != nil {
	//		return lcmd, err
	//	}
	//	lcmd, err = DoLogin(opt, lcmd, "Oracle")
	//	if err != nil {
	//		return lcmd, err
	//	}
	//}
	return lcmd, nil
}

func (opt *sqlOption) OracleDataSourceName() string {
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		opt.Host,
		opt.Port,
		opt.Username,
		opt.Password,
		opt.DBName,
	)
}

func checkOracleAccount(args *sqlOption) error {
	return checkDatabaseAccountValidate("oracle", args.OracleDataSourceName())
}
