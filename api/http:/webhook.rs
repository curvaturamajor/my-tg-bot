use serde::{Deserialize, Serialize};
use serde_json::json;
use vercel_runtime::{run, Body, Error, Request, Response, StatusCode};

// --- KONFİGÜRASYON ---
// Engellemek istediğiniz kişilerin Telegram ID numaraları
const TARGET_USER_IDS: &[i64] = &[123456789, 987654321]; 

// --- TELEGRAM JSON MODELLERİ ---
#[derive(Deserialize, Debug)]
struct Update {
    message: Option<Message>,
}

#[derive(Deserialize, Debug)]
struct Message {
    message_id: i64,
    chat: Chat,
    from: Option<User>,
    text: Option<String>,
    caption: Option<String>,
    entities: Option<Vec<MessageEntity>>,
    caption_entities: Option<Vec<MessageEntity>>,
}

#[derive(Deserialize, Debug)]
struct Chat {
    id: i64,
}

#[derive(Deserialize, Debug)]
struct User {
    id: i64,
}

#[derive(Deserialize, Debug)]
struct MessageEntity {
    #[serde(rename = "type")]
    entity_type: String,
    url: Option<String>,
    offset: usize,
    length: usize,
}

#[tokio::main]
async fn main() -> Result<(), Error> {
    run(handler).await
}

pub async fn handler(req: Request) -> Result<Response<Body>, Error> {
    // Sadece POST isteklerini kabul et
    if req.method() != "POST" {
        return Ok(Response::builder()
            .status(StatusCode::METHOD_NOT_ALLOWED)
            .body(Body::Empty)?);
    }

    let body_bytes = req.into_body();
    let update: Update = match serde_json::from_slice(&body_bytes) {
        Ok(up) => up,
        Err(_) => {
            return Ok(Response::builder()
                .status(StatusCode::BAD_REQUEST)
                .body(Body::Text("Invalid JSON".into()))?);
        }
    };

    if let Some(msg) = update.message {
        if let Some(from_user) = &msg.from {
            // 1. Adım: Mesajı atan kişi hedef listede mi?
            if TARGET_USER_IDS.contains(&from_user.id) {
                let mut contains_invite_link = false;

                // Hem normal metindeki hem de medyanın caption kısmındaki entity'leri tara
                let mut all_entities = Vec::new();
                if let Some(mut e) = msg.entities { all_entities.append(&mut e); }
                if let Some(mut ce) = msg.caption_entities { all_entities.append(&mut ce); }

                // 2. Adım: Entity türlerini ve içeriklerini kontrol et
                for entity in all_entities {
                    // Durum A: Direkt text olarak "t.me/+" veya "t.me/joinchat" yazılmışsa (text_link veya url)
                    if entity.entity_type == "url" {
                        if let Some(text_content) = get_entity_text(&msg, entity.offset, entity.length) {
                            if is_invite_link(&text_content) {
                                contains_invite_link = true;
                                break;
                            }
                        }
                    }
                    // Durum B: Bir kelimeye veya emojiye link gömülmüşse (text_link)
                    else if entity.entity_type == "text_link" {
                        if let Some(url) = entity.url {
                            if is_invite_link(&url) {
                                contains_invite_link = true;
                                break;
                            }
                        }
                    }
                }

                // Eğer link bulunduysa Telegram API'ye silme isteği gönder
                if contains_invite_link {
                    if let Ok(bot_token) = std::env::var("TELEGRAM_BOT_TOKEN") {
                        let client = reqwest::Client::new();
                        let url = format!("https://api.telegram.org/bot{}/deleteMessage", bot_token);
                        
                        let _ = client.post(&url)
                            .json(&json!({
                                "chat_id": msg.chat.id,
                                "message_id": msg.message_id
                            }))
                            .send()
                            .await;
                    }
                }
            }
        }
    }

    // Vercel serverless fonksiyonunun başarıyla sonlanması için 200 OK dönüyoruz
    Ok(Response::builder()
        .status(StatusCode::OK)
        .body(Body::Text("OK".into()))?)
}

/// Gelen URL veya metnin Telegram davet linki formatında olup olmadığını kontrol eder.
fn is_invite_link(text: &str) -> bool {
    let lower = text.to_lowercase();
    // t.me/joinchat veya t.me/+ formatındaki davet linklerini yakalar
    lower.contains("t.me/joinchat") || lower.contains("t.me/+")
}

/// UTF-16 tabanlı Telegram offset değerlerine göre metinden ilgili entity kısmını güvenli bir şekilde keser.
fn get_entity_text(msg: &Message, offset: usize, length: usize) -> Option<String> {
    // Öncelik text alanındadır, yoksa caption alanına bakılır
    let target_text = msg.text.as_ref().or(msg.caption.as_ref())?;
    
    // Telegram offsetleri UTF-16 karakter dizilimine göredir. Rust ise UTF-8 kullanır.
    // Emoji ve özel karakterlerde kayma olmaması için UTF-16'ya çevirip kesiyoruz.
    let utf16: Vec<u16> = target_text.encode_utf16().collect();
    if offset + length <= utf16.len() {
        String::from_utf16(&utf16[offset..offset + length]).ok()
    } else {
        None
    }
}
