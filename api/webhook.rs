use serde::Deserialize;
use serde_json::json;
use vercel_runtime::{
    http::{StatusCode, Response},
    run, Error, Request, ResponseBody, ServiceFn,
};

// --- KONFİGÜRASYON ---
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
    // Vercel 2.2.0 bizden handler fonksiyonunu bir ServiceFn içine sarmalamamızı bekliyor
    run(ServiceFn::new(handler)).await
}

pub async fn handler(req: Request) -> Result<Response<ResponseBody>, Error> {
    // 1. Sadece POST isteklerini kabul et
    if req.method() != "POST" {
        return Ok(Response::builder()
            .status(StatusCode::METHOD_NOT_ALLOWED)
            .body(ResponseBody::empty())?);
    }

    // 2. Hatayı Çözen Kritik Kısım: Gelen hyper::body::Incoming stream'ini byte dizisine topluyoruz
    let body_bytes = match vercel_runtime::into_bytes(req.into_body()).await {
        Ok(bytes) => bytes,
        Err(_) => {
            return Ok(Response::builder()
                .status(StatusCode::BAD_REQUEST)
                .body(ResponseBody::from("Gövde okunamadı"))?);
        }
    };

    // 3. JSON Ayrıştırma
    let update: Update = match serde_json::from_slice(&body_bytes) {
        Ok(up) => up,
        Err(_) => {
            return Ok(Response::builder()
                .status(StatusCode::BAD_REQUEST)
                .body(ResponseBody::from("Geçersiz JSON"))?);
        }
    };

    // 4. Bot Mantığı (Link Kontrolü ve Silme)
    if let Some(msg) = update.message {
        if let Some(from_user) = &msg.from {
            if TARGET_USER_IDS.contains(&from_user.id) {
                let mut contains_invite_link = false;

                let mut all_entities = Vec::new();
                if let Some(mut e) = msg.entities { all_entities.append(&mut e); }
                if let Some(mut ce) = msg.caption_entities { all_entities.append(&mut ce); }

                for entity in all_entities {
                    if entity.entity_type == "url" {
                        if let Some(text_content) = get_entity_text(&msg, entity.offset, entity.length) {
                            if is_invite_link(&text_content) {
                                contains_invite_link = true;
                                break;
                            }
                        }
                    } else if entity.entity_type == "text_link" {
                        if let Some(url) = entity.url {
                            if is_invite_link(&url) {
                                contains_invite_link = true;
                                break;
                            }
                        }
                    }
                }

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

    // Sorunsuz bittiğinde 200 OK dönüyoruz
    Ok(Response::builder()
        .status(StatusCode::OK)
        .body(ResponseBody::from("OK"))?)
}

fn is_invite_link(text: &str) -> bool {
    let lower = text.to_lowercase();
    lower.contains("t.me/joinchat") || lower.contains("t.me/+")
}

fn get_entity_text(msg: &Message, offset: usize, length: usize) -> Option<String> {
    let target_text = msg.text.as_ref().or(msg.caption.as_ref())?;
    let utf16: Vec<u16> = target_text.encode_utf16().collect();
    if offset + length <= utf16.len() {
        String::from_utf16(&utf16[offset..offset + length]).ok()
    } else {
        None
    }
}
