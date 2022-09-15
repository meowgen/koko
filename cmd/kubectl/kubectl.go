package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/meowgen/koko/pkg/utils"
)

const (
	commandName = "rawkubectl"
)

func main() {
	args := os.Args[1:]
	var s strings.Builder
	for i := range args {
		s.WriteString(args[i])
		s.WriteString(" ")
	}
	commandPrefix := commandName
	token, _ := utils.GetDecryptedToken()
	if token != "" {
		token = strings.ReplaceAll(token, "'", "")
		commandPrefix = fmt.Sprintf(`%s --token='%s'`, commandName, token)
	}

	commandString := fmt.Sprintf("%s %s", commandPrefix, s.String())

	utils.WrappedExec(commandString, token)
}
