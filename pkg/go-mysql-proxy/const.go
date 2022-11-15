package mysqlProxy

const (
	ComQuit byte = iota + 1
	comInitDB
	ComQuery
	ComFieldList
	comCreateDB
	comDropDB
	comRefresh
	comShutdown
	comStatistics
	comProcessInfo
	comConnect
	comProcessKill
	comDebug
	comPing
	comTime
	comDelayedInsert
	comChangeUser
	comBinlogDump
	comTableDump
	comConnectOut
	comRegisterSlave
	ComStmtPrepare
	ComStmtExecute
	comStmtSendLongData
	ComStmtClose
	comStmtReset
	comSetOption
	comStmtFetch
)
