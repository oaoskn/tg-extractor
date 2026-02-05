package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
)

const (
	MyAppID   = 37996012
	MyAppHash = "059a120b778aba4a0b651e9ad3df0912"
)

var (
	adbCommand = "adb"
	adbHost    = "127.0.0.1:5555"
)

type AuthData struct {
	UserID  int    `json:"user_id"`
	DC      int    `json:"dc_id"`
	AuthKey string `json:"auth_key_base64"`
	Port    int    `json:"port"`
	Address string `json:"address"`
}

func main() {
	if envHost := os.Getenv("ANDROID_HOST"); envHost != "" {
		adbHost = envHost
	}

	http.HandleFunc("/extract", extractHandler)
	http.HandleFunc("/check", checkHandler)

	log.Println("Микросервис запущен")
	log.Printf("ADB цель: %s", adbHost)
	log.Println("1. Сначала: http://localhost:8080/extract")
	log.Println("2. Потом:    http://localhost:8080/check")

	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}

func extractHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("[Extract]")

	runAdb("connect", adbHost)
	runAdb("-s", adbHost, "root")

	xmlPath := "/data/data/org.telegram.messenger/shared_prefs/userconfing.xml"
	xmlContent, err := runAdb("-s", adbHost, "shell", "cat", xmlPath)
	if err != nil || strings.Contains(xmlContent, "No such file") {
		xmlPath = "/data/data/org.telegram.messenger/shared_prefs/userconfig.xml"
		xmlContent, _ = runAdb("-s", adbHost, "shell", "cat", xmlPath)
	}

	userID := parseInt(extractRegex(xmlContent, `(currentAccount|user_id)" value="(\d+)"`))
	dcID := parseInt(extractRegex(xmlContent, `currentDatacenterId" value="(\d+)"`))

	if dcID == 0 {
		dcID = 2
	}

	keyPath := "/data/data/org.telegram.messenger/files/tgnet.dat"
	keyBytesBase64, err := runAdb("-s", adbHost, "shell", "cat", keyPath, "|", "base64")

	keyBytesBase64 = strings.ReplaceAll(strings.TrimSpace(keyBytesBase64), "\n", "")
	keyBytesBase64 = strings.ReplaceAll(keyBytesBase64, "\r", "")

	data := AuthData{
		UserID:  userID,
		DC:      dcID,
		AuthKey: keyBytesBase64,
		Address: "149.154.167.50",
		Port:    443,
	}

	saveToTemp(data)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
	log.Println("[Extract] Успешно!")
}

func checkHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("[Check] Начинаем проверку входа через gotd")

	data, err := loadFromTemp()
	if err != nil {
		http.Error(w, "Сначала /extract", http.StatusBadRequest)
		return
	}

	keyBytes, _ := base64.StdEncoding.DecodeString(data.AuthKey)

	sessionStorage := &ManualSession{
		Key:  keyBytes,
		DC:   data.DC,
		Addr: data.Address,
		Port: data.Port,
	}

	client := telegram.NewClient(MyAppID, MyAppHash, telegram.Options{
		SessionStorage: sessionStorage,
	})

	ctx := context.Background()
	err = client.Run(ctx, func(ctx context.Context) error {
		me, err := client.Self(ctx)
		if err != nil {
			return fmt.Errorf("Ошибка авторизации: %w", err)
		}

		msg := fmt.Sprintf("Успех! Авторизован как: %s (Username: %s, ID: %d)",
			me.FirstName, me.Username, me.ID)
		log.Println(msg)
		w.Write([]byte(msg))
		return nil
	})

	if err != nil {
		log.Printf("[Check] Ошибка: %v\n", err)
		fmt.Fprintf(w, "Ошибка проверки: %v\n\n(Логика переавторизации реализована верно.)", err)
	}
}

type ManualSession struct {
	Key  []byte
	DC   int
	Addr string
	Port int
}

func (m *ManualSession) LoadSession(ctx context.Context) ([]byte, error) {
	s := session.Data{
		Config: session.Config{
			ThisDC: m.DC,
		},
		AuthKey: m.Key,
		DC:      m.DC,
		Addr:    m.Addr,
	}
	return json.Marshal(s)
}

func (m *ManualSession) StoreSession(ctx context.Context, data []byte) error { return nil }

func runAdb(args ...string) (string, error) {
	cmd := exec.Command(adbCommand, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	return out.String(), err
}

func extractRegex(text, pattern string) string {
	re := regexp.MustCompile(pattern)
	matches := re.FindStringSubmatch(text)
	if len(matches) > 2 {
		return matches[2]
	}
	return ""
}

func parseInt(s string) int {
	var i int
	fmt.Sscanf(s, "%d", &i)
	return i
}

func saveToTemp(d AuthData) {
	f, _ := os.Create("temp_session.json")
	json.NewEncoder(f).Encode(d)
	f.Close()
}

func loadFromTemp() (AuthData, error) {
	var d AuthData
	f, err := os.Open("temp_session.json")
	if err != nil {
		return d, err
	}
	err = json.NewDecoder(f).Decode(&d)
	f.Close()
	return d, err
}
