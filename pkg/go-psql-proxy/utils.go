package psqlProxy

import (
	"math/rand"
)

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

func randStringRunes(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

func createSalt() []byte {
	saltByte := make([]byte, 8)
	rand.Read(saltByte)
	salt1 := []byte(randStringRunes(8))

	salt2Byte := make([]byte, 12)
	rand.Read(salt2Byte)
	salt2 := []byte(randStringRunes(12))
	saltFull := append(salt1, salt2...)
	return saltFull
}
