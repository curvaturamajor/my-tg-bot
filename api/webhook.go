package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"time"
	"unicode/utf16"
)

// --- YAPILANDIRMA ---
// Gruplarda linkleri silinecek hedef kişilerin ID'leri
var targetUserIDs = []int64{7350150331, 987654321}

// TCP tünellerini sıcak tutarak cold start sonrası tepki süresini milisaniyelere düşürür
var httpClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        15,
		MaxIdleConnsPerHost: 15,
		IdleConnTimeout:     90 * time.Second,
	},
}

// --- MİNİMAL JSON MODELLERİ (Zero-Overhead için sadece gerekli alanlar) ---
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

	// Sıfır bellek yükü için doğrudan Request Body'den akış şeklinde decode ediyoruz
	var update Update
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	if update.Message != nil {
		msg := update.Message

		// Sadece grup ve süpergrupları filtrele
		if msg.Chat.Type == "group" || msg.Chat.Type == "supergroup" {
			if msg.From != nil {
				userID := msg.From.ID

				// Hedef kullanıcı kontrolü (Performans için düz döngü)
				isTarget := false
				for i := 0; i < len(targetUserIDs); i++ {
					if targetUserIDs[i] == userID {
						isTarget = true
						break
					}
				}

				if isTarget {
					containsLink := false

					// 1. Durum: Düz metin içindeki linkler (Entities)
					if len(msg.Entities) > 0 {
						containsLink = checkEntities(msg, msg.Entities)
					}
					// 2. Durum: Medya altındaki açıklamalar (Caption Entities)
					if !containsLink && len(msg.CaptionEntities) > 0 {
						containsLink = checkEntities(msg, msg.CaptionEntities)
					}

					// Link tespit edildiyse, Vercel süreci dondurmadan ÖNCE senkron olarak sil
					if containsLink {
						deleteMessage(botToken, msg.Chat.ID, msg.MessageID)
					}
				}
			}
		}
	}

	// Vercel'e işimizin bittiğini ve her şeyin başarılı olduğunu bildiriyoruz
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// --- TARAMA VE ANALİZ MOTORU ---
func checkEntities(msg *Message, entities []MessageEntity) bool {
	for i := 0; i < len(entities); i++ {
		entity := entities[i]
		
		// Normal URL (t.me/...)
		if entity.Type == "url" {
			textContent := getEntityText(msg, entity.Offset, entity.Length)
			if isInviteLink(textContent) {
				return true
			}
		} else if entity.Type == "text_link" {
			// Maskelenmiş Hypertext linkler ([Tıkla](t.me/...))
			if isInviteLink(entity.URL) {
				return true
			}
		}
	}
	return false
}

// Telegram'ın UTF-16 indeksleme standardıyla %100 uyumlu güvenli kesim fonksiyonu
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

// Regex kullanmadan, bellek harcamadan (O(n)) çalışan t.me/ analizörü
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
			return true
		}
	}
	return false
}

// --- TELEGRAM API SİLME İŞLEMİ (SENKRON) ---
func deleteMessage(token string, chatID int64, messageID int64) {
	// Vercel'in ağı kesmemesi için 4 saniyelik güvenli bir pencere açıyoruz
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	// String birleştirmelerinde heap allocation yapmamak için buffer kullanımı
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
