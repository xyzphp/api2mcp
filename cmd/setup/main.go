package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

func main() {
	template, err := os.ReadFile(".env.example")
	if err != nil {
		panic(err)
	}
	secret := func() string {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			panic(err)
		}
		return base64.StdEncoding.EncodeToString(b)
	}
	content := strings.ReplaceAll(string(template), "CHANGE_ME_ADMIN_TOKEN", secret())
	content = strings.ReplaceAll(content, "CHANGE_ME_ENCRYPTION_KEY", secret())
	file, err := os.OpenFile(".env", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		fmt.Println(".env 已存在，保留现有 Token 和加密密钥。")
		return
	}
	if err != nil {
		panic(err)
	}
	if _, err := file.WriteString(content); err != nil {
		panic(err)
	}
	if err := file.Close(); err != nil {
		panic(err)
	}
	fmt.Println("已生成 .env（权限 0600）。用其中的 ADMIN_TOKEN 登录，请保留 ENCRYPTION_KEY。")
}
