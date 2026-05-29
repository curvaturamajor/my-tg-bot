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

var targetUserIDs = []int64{7350150331, 115180296, 8135840643}

// OPTİMİZASYON 1: Global HTTP Client ve Bağlantı Havuzu Havuzu
// MaxIdleConnsPerHost sayesinde Telegram sunucularıyla TCP bağlantısı hep sıcak tutulur.
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

// --- TELEGRAM MİNİMAL JSON MODELLERİ ---
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

	update := updatePool.Get().(*Update)	
	update.Message = nil 

	err := json.NewDecoder(r.Body).Decode(update)
	if err != nil {
		updatePool.Put(update)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if update.Message != nil && update.Message.From != nil {
		userID := update.Message.From.ID

		isTarget := false
		for i := 0; i < len(targetUserIDs); i++ {
			if targetUserIDs[i] == userID {
				isTarget = true
				break
			}
		}

		if isTarget {
			msg := update.Message
			containsLink := false

			if len(msg.Entities) > 0 {
				containsLink = checkEntities(msg, msg.Entities)
			}
			if !containsLink && len(msg.CaptionEntities) > 0 {
				containsLink = checkEntities(msg, msg.CaptionEntities)
			}

			if containsLink {
				botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
				if botToken != "" {
					// Arka plan goroutine'ine geçiyoruz
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

// OPTİMİZASYON 3: Keskin Nokta Taramalı Zero-Allocation Link Kontrolü
func isInviteLink(text string) bool {
	n := len(text)
	if n < 6 { 
		return false
	}

	// Tüm string'i 5'erli bloklar halinde taramak yerine, önce '.' avlıyoruz.
	// Bu işlemci seviyesindeki arama döngüsünü inanılmaz hızlandırır.
	for i := 1; i < n-3; i++ {
		if text[i] == '.' {
			// Noktanın solunda 't' veya 'T', sağında 'm' ve 'e' var mı?
			if (text[i-1] == 't' || text[i-1] == 'T') &&
				(text[i+1] == 'm' || text[i+1] == 'M') &&
				(text[i+2] == 'e' || text[i+2] == 'E') &&
				(text[i+3] == '/') {
				
				// "t.me/" yakalandı, sonrasını kontrol et
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

// OPTİMİZASYON 4: Sıcak TCP Bağlantısı ve Context Korumalı İstek
func deleteMessage(token string, chatID int64, messageID int64) {
	// Goroutine'in havada asılı kalmaması için 2 saniyelik sert timeout context'i
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

	// Global ve sıcak TCP bağlantısını kullanan client ile isteği atıyoruz
	resp, err := httpClient.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}
