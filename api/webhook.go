package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"sync"
	"unicode/utf16"
)

var targetUserIDs = []int64{7350150331, 987654321}
// Her istek geldiğinde sıfırdan obje yaratıp GC'yi yormamak için nesneleri reuse ediyoruz.
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

// Vercel Native Giriş Noktası
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

// ZERO-ALLOCATION LINK KONTROLÜ
// strings.ToLower kullanmadan, heap allocation yapmadan case-insensitive ham kontrol yapar.
func isInviteLink(text string) bool {
	if len(text) < 5 { // "t.me/+" minimum 6 karakterdir
		return false
	}

	for i := 0; i <= len(text)-5; i++ {
		// "t.me/" kontrolü (büyük/küçük harf esnek)
		if (text[i] == 't' || text[i] == 'T') &&
			(text[i+1] == '.') &&
			(text[i+2] == 'm' || text[i+2] == 'M') &&
			(text[i+3] == 'e' || text[i+3] == 'E') &&
			(text[i+4] == '/') {
			
			// t.me/ kısmından sonrasına bakıyoruz
			rem := text[i+5:]
			if len(rem) >= 8 && (rem[:8] == "joinchat" || rem[:8] == "JOINCHAT") {
				return true
			}
			if len(rem) > 0 && rem[0] == '+' {
				return true
			}
		}
	}
	return false
}
// EN HIZLI VE HAM HTTP POST ÇAĞRISI
func deleteMessage(token string, chatID int64, messageID int64) {
	// String birleştirme maliyetini sıfırlamak için buffer optimizasyonu
	var urlBuf bytes.Buffer
	urlBuf.WriteString("https://api.telegram.org/bot")
	urlBuf.WriteString(token)
	urlBuf.WriteString("/deleteMessage")

	// İsteğin gövdesini (payload) elle (manual) ve allocation yapmadan çiziyoruz
	var jsonBuf bytes.Buffer
	jsonBuf.WriteString(`{"chat_id":`)
	jsonBuf.WriteString(strconv.FormatInt(chatID, 10))
	jsonBuf.WriteString(`,"message_id":`)
	jsonBuf.WriteString(strconv.FormatInt(messageID, 10))
	jsonBuf.WriteString(`}`)

	resp, err := http.Post(urlBuf.String(), "application/json", &jsonBuf)
	if err == nil {
		resp.Body.Close()
	}
}
