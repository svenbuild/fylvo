//! Timings of the inventory as a sync uses it. Run with
//! `cargo test --release -p cloudreve-sync inventory_timings -- --ignored --nocapture`.

use super::InventoryDb;
use crate::inventory::{MetadataEntry, NewTaskRecord, TaskStatus, TaskUpdate};
use diesel::RunQueryDsl;
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
    timed("query_2000_paths_batch", || {
        let paths: Vec<&str> = paths.iter().map(String::as_str).collect();
        assert_eq!(db.query_by_paths(&paths).unwrap().len(), FILES);
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
    // A long running app: every synced file left a finished task.
    diesel::sql_query(
        "WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM n WHERE x < 100000) \
         INSERT INTO task_queue (id, drive_id, task_type, local_path, status, created_at, updated_at) \
         SELECT 'old' || x, 'd', 'upload', '/S/old' || x, 'completed', x, x FROM n",
    )
    .execute(&mut db.connection().unwrap())
    .unwrap();
    timed("recent_tasks_100_polls", || {
        for _ in 0..100 {
            assert_eq!(db.query_recent_tasks(None).unwrap().finished.len(), 25);
        }
    });
}
