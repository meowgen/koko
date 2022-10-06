package srvconn

import (
	_ "github.com/mattn/go-sqlite3"
	"github.com/meowgen/koko/pkg/localcommand"
)

func NewSQLiteConnection(ops ...SqlOption) (*SQLiteConn, error) {
	args := &sqlOption{
		win: Windows{
			Width:  80,
			Height: 120,
		},
	}
	for _, setter := range ops {
		setter(args)
	}
	lCmd, err := startSQLiteCommand(args)
	if err != nil {
		return nil, err
	}
	err = lCmd.SetWinSize(args.win.Width, args.win.Height)
	if err != nil {
		_ = lCmd.Close()
		return nil, err
	}
	return &SQLiteConn{options: args, LocalCommand: lCmd}, nil
}

func startSQLiteCommand(opt *sqlOption) (lcmd *localcommand.LocalCommand, err error) {
	argv := opt.SQLiteCommandArgs()
	lcmd, err = localcommand.New("sqlite3", argv, localcommand.WithPtyWin(opt.win.Width, opt.win.Height))
	if err != nil {
		return nil, err
	}
	return lcmd, nil

}

func (opt *sqlOption) SQLiteCommandArgs() []string {
	return []string{
		opt.PathToDb,
	}
}

func (conn *SQLiteConn) KeepAlive() error {
	return nil
}
func (conn *SQLiteConn) Close() error {
	_, _ = conn.Write([]byte(".exit"))
	return conn.LocalCommand.Close()
}

type SQLiteConn struct {
	options *sqlOption
	*localcommand.LocalCommand
}

func SqlPathToDb(path string) SqlOption {
	return func(args *sqlOption) {
		args.PathToDb = path
	}
}

func SqlCreateDbIfNotExist(flag bool) SqlOption {
	return func(args *sqlOption) {
		args.CreateDbIfNotExist = flag
	}
}
