use std::io::Write;
use std::sync::atomic::{AtomicU64, Ordering};

use chrono::{SecondsFormat, Utc};
use serde_json::json;

static SEQUENCE: AtomicU64 = AtomicU64::new(0);

// 宿主只记录自己的事件；子进程继承标准流，保留原事件身份和级别。
pub fn emit(level: &str, event: &str, message: &str, code: Option<&str>) {
    let now = Utc::now();
    let sequence = SEQUENCE.fetch_add(1, Ordering::Relaxed);
    let mut record = json!({
        "id": format!("log_{}_{}_{}", std::process::id(), now.timestamp_micros(), sequence),
        "timestamp": now.to_rfc3339_opts(SecondsFormat::Millis, true),
        "level": level, "source": "desktop", "event": format!("desktop.{event}"), "message": message
    });
    if let Some(code) = code {
        record["errorCode"] = code.into();
    }
    let _ = writeln!(std::io::stderr().lock(), "{record}");
}
