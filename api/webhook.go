package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
	"unicode/utf16"
)

var targetUserIDs = []int64{7350150331, 987654321}

var httpClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
	},
}

var updatePool = sync.Pool{
	New: func() interface{} {
		return &Update{}
	},
}

type MessageEntity struct {
	Type   string `json:"type"`
	URL    string `json:"url,omitempty"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}

type User struct {
	ID int64 `json:"id"`
}

type Chat struct {
	ID int64 `json:"id"`
}

type Message struct {
	MessageID       int64           `json:"message_id"`
	Chat            Chat            `json:"chat"`
	From            *User           `json:"from,omitempty"`
	Text            string          `json:"text,omitempty"`
	Caption         string          `json:"caption,omitempty"`
	Entities        []MessageEntity `json:"entities,omitempty"`
	CaptionEntities []MessageEntity `json:"caption_entities,omitempty"`
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
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("HATA: Token Yok"))
		return
	}

	// --- TEŞHİS: HAM JSON VERİSİNİ OKUMA VE LOGLAMA ---
	// İstek gövdesini (body) okuyoruz
	var bodyBytes []byte
	if r.Body != nil {
		buf := new(bytes.Buffer)
		buf.ReadFrom(r.Body)
		bodyBytes = buf.Bytes()
	}

	// Okuduğumuz veriyi Vercel loguna basıyoruz
	log.Printf("HAM TELEGRAM VERISI: %s", string(bodyBytes))

	// JSON Decoder'ın okuyabilmesi için body'yi yeniden dolduruyoruz
r.Body = http.MaxBytesReader(w, io.NopCloser(bytes.NewReader(bodyBytes)), 1048576)

	update := updatePool.Get().(*Update)	
	update.Message = nil 

	// Ham veriyi çözmeyi dene
	err := json.Unmarshal(bodyBytes, update)
	if err != nil {
		updatePool.Put(update)
		w.WriteHeader(http.StatusOK) // Telegram'ı susturmak için 200 dönüyoruz
		return
	}

	if update.Message != nil {
		msg := update.Message

		// /start Kontrolü
		if msg.Text == "/start" {
			go sendOkResponse(botToken, msg.Chat.ID)
		}

		// Link Silme Kontrolü
		if msg.From != nil {
			userID := msg.From.ID

			isTarget := false
			for i := 0; i < len(targetUserIDs); i++ {
				if targetUserIDs[i] == userID {
					isTarget = true
					break
				}
			}

			if isTarget {
				containsLink := false

				if len(msg.Entities) > 0 {
					containsLink = checkEntities(msg, msg.Entities)
				}
				if !containsLink && len(msg.CaptionEntities) > 0 {
					containsLink = checkEntities(msg, msg.CaptionEntities)
				}

				if containsLink {
					go deleteMessage(botToken, msg.Chat.ID, msg.MessageID)
				}
			}
		}
	}

	updatePool.Put(update)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func checkEntities(msg *Message, entities []MessageEntity) bool {
	for i := 0; i < len(entities); i++ {
		entity := entities[i]
		if entity.Type == "url" {
			textContent := getEntityText(msg, entity.Offset, entity.Length)
			if isInviteLink(textContent) {
				return true
			}
		} else if entity.Type == "text_link" {
			if isInviteLink(entity.URL) {
				return true
			}
		}
	}
	return false
}

func getEntityText(msg *Message, offset, length int) string {
	text := msg.Text
	if text == "" {
		text = msg.Caption
	}
	if text == "" {
		return ""
	}

	utf16Text := utf16.Encode([]rune(text))
	if offset+length <= len(utf16Text) {
		return string(utf16.Decode(utf16Text[offset : offset+length]))
	}
	return ""
}

func isInviteLink(text string) bool {
	n := len(text)
	if n < 5 { 
		return false
	}

	for i := 0; i <= n-5; i++ {
		if (text[i] == 't' || text[i] == 'T') &&
			(text[i+1] == '.') &&
			(text[i+2] == 'm' || text[i+2] == 'M') &&
			(text[i+3] == 'e' || text[i+3] == 'E') &&
			(text[i+4] == '/') {
			
			remaining := text[i+5:]
			
			for j := 0; j <= len(remaining)-8; j++ {
				if (remaining[j] == 'j' || remaining[j] == 'J') &&
					(remaining[j+1] == 'o' || remaining[j+1] == 'O') &&
					(remaining[j+2] == 'i' || remaining[j+2] == 'I') &&
					(remaining[j+3] == 'n' || remaining[j+3] == 'N') &&
					(remaining[j+4] == 'c' || remaining[j+4] == 'C') &&
					(remaining[j+5] == 'h' || remaining[j+5] == 'H') &&
					(remaining[j+6] == 'a' || remaining[j+6] == 'A') &&
					(remaining[j+7] == 't' || remaining[j+7] == 'T') {
					return true
				}
			}

			for j := 0; j < len(remaining); j++ {
				if remaining[j] == '+' {
					return true
				}
			}
		}
	}
	return false
}

func deleteMessage(token string, chatID int64, messageID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var urlBuf bytes.Buffer
	urlBuf.WriteString("https://api.telegram.org/bot")
	urlBuf.WriteString(token)
	urlBuf.WriteString("/deleteMessage")

	var jsonBuf bytes.Buffer
	jsonBuf.WriteString(`{"chat_id":`)
	jsonBuf.WriteString(strconv.FormatInt(chatID, 10))
	jsonBuf.WriteString(`,"message_id":`)
	jsonBuf.WriteString(strconv.FormatInt(messageID, 10))
	jsonBuf.WriteString(`}`)

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

func sendOkResponse(token string, chatID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
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
