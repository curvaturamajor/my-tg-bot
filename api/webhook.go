package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

var httpClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 10,
	},
}

var updatePool = sync.Pool{
	New: func() interface{} {
		return &Update{}
	},
}

type Chat struct {
	ID int64 `json:"id"`
}

type Message struct {
	Text string `json:"text,omitempty"`
	Chat Chat   `json:"chat"`
}

type Update struct {
	Message *Message `json:"message,omitempty"`
}

func Handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	if botToken == "" {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("HATA: TOKEN BULUNAMADI"))
		return
	}

	update := updatePool.Get().(*Update)
	update.Message = nil

	err := json.NewDecoder(r.Body).Decode(update)
	if err != nil {
		updatePool.Put(update)
		w.WriteHeader(http.StatusOK)
		return
	}

	if update.Message != nil && update.Message.Text == "/start" {
		// Race condition olmaması için doğrudan senkron çağırıyoruz,
		// havuz temizlenmeden önce mesajın gitmesini garanti ediyoruz.
		sendOkResponse(botToken, update.Message.Chat.ID)
	}

	updatePool.Put(update)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func sendOkResponse(token string, chatID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var urlBuf bytes.Buffer
	urlBuf.WriteString("https://api.telegram.org/bot")
	urlBuf.WriteString(token)
	urlBuf.WriteString("/sendMessage")

	var jsonBuf bytes.Buffer
	jsonBuf.WriteString(`{"chat_id":`)
	jsonBuf.WriteString(strconv.FormatInt(chatID, 10))
	jsonBuf.WriteString(`,"text":"OK!"}`)

	req, err := http.NewRequestWithContext(ctx, "POST", urlBuf.String(), &jsonBuf)
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}
