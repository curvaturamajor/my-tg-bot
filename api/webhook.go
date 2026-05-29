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

// --- YAPILANDIRMA ---
// Gruplarda linkleri silinecek hedef kişilerin ID'leri
var targetUserIDs = []int64{7350150331, 987654321}

// Bağlantı havuzu: TCP tünellerini sıcak tutarak anlık tepki süresini garantiler
var httpClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	},
}

// Bellek dostu Update havuzu: Her webhook isteğinde yeniden struct yaratılmasını engeller
var updatePool = sync.Pool{
	New: func() interface{} {
		return &Update{}
	},
}

// --- MİNİMAL MODELLER ---
type MessageEntity struct {
	Type    string `json:"type"`
	URL     string `json:"url,omitempty"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}

type User struct {
	ID int64 `json:"id"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
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

// --- ANA İŞLEYİCİ ---
func Handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	if botToken == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// 1. ADIM: Telegram'ı bekletmemek için OK cevabını anında gönderiyoruz.
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))

	// 2. ADIM: Vercel'in r.Body'yi hemen temizlememesi için veriyi belleğe kopyalıyoruz.
	bodyBuf := new(bytes.Buffer)
	bodyBuf.ReadFrom(r.Body)
	r.Body.Close()
	reqBody := bodyBuf.Bytes()

	// 3. ADIM: Tüm işleme ve silme mantığını tek bir Goroutine içinde asenkron yürütüyoruz.
	go func(data []byte) {
		update := updatePool.Get().(*Update)
		update.Message = nil

		err := json.Unmarshal(data, update)
		if err != nil {
			updatePool.Put(update)
			return
		}

		if update.Message != nil {
			msg := update.Message

			// GRUP İÇİ LİNK DENETİMİ (Yalnızca grup ve süpergruplar işlenir)
			if msg.Chat.Type == "group" || msg.Chat.Type == "supergroup" {
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

						// Normal metin içindeki linkleri (entities) veya hypertextleri tarar
						if len(msg.Entities) > 0 {
							containsLink = checkEntities(msg, msg.Entities)
						}
						// Medya dosyalarının altındaki açıklamaları (caption) tarar
						if !containsLink && len(msg.CaptionEntities) > 0 {
							containsLink = checkEntities(msg, msg.CaptionEntities)
						}

						// Link yakalandıysa, mesajı imha et
						if containsLink {
							deleteMessage(botToken, msg.Chat.ID, msg.MessageID)
						}
					}
				}
			}
		}
		updatePool.Put(update)
	}(reqBody)
}

// --- TARAMA VE ANALİZ MOTORLARI ---
func checkEntities(msg *Message, entities []MessageEntity) bool {
	for i := 0; i < len(entities); i++ {
		entity := entities[i]
		
		// Düz metin halindeki link (Örn: t.me/joinchat/...)
		if entity.Type == "url" {
			textContent := getEntityText(msg, entity.Offset, entity.Length)
			if isInviteLink(textContent) {
				return true
			}
		} else if entity.Type == "text_link" {
			// Hypertext formatındaki gizli link (Örn: [Tıklayın](t.me/...))
			if isInviteLink(entity.URL) {
				return true
			}
		}
	}
	return false
}

// Telegram entity'leri UTF-16 indeksleri kullandığı için kesin karakter kesimi yapar
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

// Demode olan telegram.me gibi yapıları atlar, sadece "t.me/" yapısını arayarak maksimum hız sağlar
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
			return true // t.me/ yapısı tespit edildiği an tetiklenir
		}
	}
	return false
}

// --- TELEGRAM API İŞLEMLERİ ---
func deleteMessage(token string, chatID int64, messageID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
