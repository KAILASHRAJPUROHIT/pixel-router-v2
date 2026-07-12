package main

import (
	"crypto/rand"
	"fmt"
	"strings"
)

func main() {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789"
	b := make([]byte, 20)
	rand.Read(b)
	var sb strings.Builder
	for _, x := range b {
		sb.WriteByte(alphabet[int(x)%len(alphabet)])
	}
	fmt.Println(sb.String())
}
