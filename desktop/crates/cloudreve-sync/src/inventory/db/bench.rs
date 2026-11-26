//! Timings of the inventory as a sync uses it. Run with
//! `cargo test --release -p cloudreve-sync inventory_timings -- --ignored --nocapture`.

use super::InventoryDb;
use crate::inventory::{MetadataEntry, NewTaskRecord, TaskStatus, TaskUpdate};
use std::time::Instant;
use uuid::Uuid;

const FILES: usize = 2000;

fn timed(label: &str, run: impl FnOnce()) {
    let start = Instant::now();
    run();
    println!("BENCH {label} {:.1} ms", start.elapsed().as_secs_f64() * 1000.0);
}

#[test]
#[ignore]
fn inventory_timings() {
    let dir = tempfile::tempdir().unwrap();
    let db = InventoryDb::with_path(dir.path().join("meta.db")).unwrap();
    let drive = Uuid::new_v4();
    let paths: Vec<String> = (0..FILES).map(|i| format!("/S/folder{}/file{i}.txt", i % 50)).collect();

    timed("upsert_2000_rows", || {
        for path in &paths {
            let mut entry = MetadataEntry::new(drive, path.as_str(), false);
            entry.etag = "v1".into();
            db.upsert(&entry).unwrap();
        }
    });
    timed("query_2000_paths", || {
        for path in &paths {
            assert!(db.query_by_path(path).unwrap().is_some());
        }
    });
    timed("task_2000_insert_run_finish", || {
        for (i, path) in paths.iter().enumerate() {
            let id = format!("t{i}");
            assert!(db.insert_task_if_not_exist(&NewTaskRecord::new(&id, "d", "upload", path.as_str())).unwrap());
            for status in [TaskStatus::Running, TaskStatus::Completed] {
                let update = TaskUpdate { status: Some(status), ..Default::default() };
                db.update_task(&id, update).unwrap();
            }
        }
    });
}
