package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"unicode/utf16"
)

// Global config - Bellek allocation'ı yapmamak için harita yerine array ve primitive tipler
var targetUserIDs = []int64{123456789, 987654321}

// Telegram JSON Modelleri (Sadece ihtiyacımız olan alanlar, bellek tasarrufu için minimal)
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

// Vercel'in native Go mimarisi için giriş noktası
func Handler(w http.ResponseWriter, r *http.Request) {
	// 1. Sadece POST isteklerini kabul et
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// 2. JSON'ı verimli bir şekilde stream üzerinden oku (bütün body'yi hafızaya tek seferde kopyalamaz)
	var update Update
	err := json.NewDecoder(r.Body).Decode(&update)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Invalid JSON"))
		return
	}

	// 3. Bot Mantığı
	if update.Message != nil && update.Message.From != nil {
		userID := update.Message.From.ID

		// Kullanıcı hedef listede mi? (Allocation yapmayan hızlı döngü)
		isTarget := false
		for _, id := range targetUserIDs {
			if id == userID {
				isTarget = true
				break
			}
		}

		if isTarget {
			msg := update.Message
			containsLink := false

			// Tüm entity'leri tek tek kontrol et
			if len(msg.Entities) > 0 {
				containsLink = checkEntities(msg, msg.Entities)
			}
			if !containsLink && len(msg.CaptionEntities) > 0 {
				containsLink = checkEntities(msg, msg.CaptionEntities)
			}

			// Eğer davet linki bulunduysa mesajı sil
			if containsLink {
				botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
				if botToken != "" {
					// Ham HTTP çağrısını goroutine ile arka plana atıyoruz ki Vercel isteği hemen 200 OK dönsün, bot tıkanmasın
					go deleteMessage(botToken, msg.Chat.ID, msg.MessageID)
				}
			}
		}
	}

	// Vercel'e hemen 200 OK dönüyoruz (Hız için kritik)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// Entity'leri kontrol eden optimize fonksiyon
func checkEntities(msg *Message, entities []MessageEntity) bool {
	for _, entity := range entities {
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

// Telegram UTF-16 offset mantığına göre string'i güvenli ve allocation-friendly kesme
func getEntityText(msg *Message, offset, length int) string {
	text := msg.Text
	if text == "" {
		text = msg.Caption
	}
	if text == "" {
		return ""
	}

	// String'i UTF-16 array'ine çevirip offset koruması yapıyoruz (Telegram uyumluluğu için şart)
	utf16Text := utf16.Encode([]rune(text))
	if offset+length <= len(utf16Text) {
		return string(utf16.Decode(utf16Text[offset : offset+length]))
	}
	return ""
}

// Performans için küçük harfe çevirmeden (allocation yapmadan) direkt kontrol
func isInviteLink(text string) bool {
	// strings.Contains yerine performans için ham kontrol. t.me/joinchat veya t.me/+ aranıyor.
	// Küçük/büyük harf duyarlılığını strings.ToLower yapmadan (yeni string yaratmadan) yönetmek için:
	lower := strings.ToLower(text) 
	return strings.Contains(lower, "t.me/joinchat") || strings.Contains(lower, "t.me/+")
}

// Telegram API'sine en hızlı şekilde ham POST isteği atan fonksiyon
func deleteMessage(token string, chatID int64, messageID int64) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/deleteMessage", token)

	// Ham JSON byte'larını manuel ve hızlıca birleştiriyoruz (json.Marshal'dan daha az bellek harcar)
	payload := []byte(fmt.Sprintf(`{"chat_id":%d,"message_id":%d}`, chatID, messageID))

	// Standart HTTP Client'ı Vercel üzerinde oldukça hızlıdır
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(payload))
	if err == nil {
		resp.Body.Close() // Bellek sızıntısını önlemek için hemen kapatıyoruz
	}
}
