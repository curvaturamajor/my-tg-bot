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
		w.Write([]byte("Sadece POST istekleri kabul edilir."))
		return
	}

	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	if botToken == "" {
		// TELEGRAM_BOT_TOKEN okunamazsa Vercel loglarında kabak gibi 500 hatası göreceğiz
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("HATA: TELEGRAM_BOT_TOKEN yuklenemedi!"))
		return
	}

	update := updatePool.Get().(*Update)	
	update.Message = nil 

	err := json.NewDecoder(r.Body).Decode(update)
	if err != nil {
		updatePool.Put(update)
		// JSON parse edilemezse 400 hatası göreceğiz
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("HATA: JSON cozulemedi."))
		return
	}

	if update.Message != nil {
		msg := update.Message

		// --- GEÇİCİ TEST: /start KOMUTU KONTROLÜ ---
		if msg.Text == "/start" {
			go sendOkResponse(botToken, msg.Chat.ID)
		}

		// --- ORİJİNAL LİNK SİLME MANTIĞI ---
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
	if n < 6 { 
		return false
	}

	for i := 1; i < n-3; i++ {
		if text[i] == '.' {
			if (text[i-1] == 't' || text[i-1] == 'T') &&
				(text[i+1] == 'm' || text[i+1] == 'M') &&
				(text[i+2] == 'e' || text[i+2] == 'E') &&
				(text[i+3] == '/') {
				
				rem := text[i+4:]
				if len(rem) >= 8 && (rem[:8] == "joinchat" || rem[:8] == "JOINCHAT") {
					return true
				}
				if len(rem) > 0 && rem[0] == '+' {
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
