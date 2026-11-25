//! The FUSE side: each request is answered at once from memory or the local file, or handed to
//! a tokio task with a deadline (`deadline.rs`). The FUSE threads never wait on the network.

use super::callers::is_scanner;
use super::deadline::{CONTENT, METADATA};
use super::drive::Drive;
use fuser::{
    Errno, FileHandle, Filesystem, FopenFlags, Generation, INodeNo, LockOwner, OpenAccMode,
    OpenFlags, RenameFlags, ReplyAttr, ReplyCreate, ReplyData, ReplyDirectory, ReplyEmpty,
    ReplyEntry, ReplyOpen, ReplyStatfs, ReplyWrite, Request, TimeOrNow, WriteFlags,
};
use std::ffi::OsStr;
use std::sync::Arc;
use std::time::{Duration, SystemTime};

/// How long the kernel may keep names and attributes (short: the server can change them).
const TTL: Duration = Duration::from_secs(1);

pub(crate) struct DriveFs {
    pub drive: Arc<Drive>,
}

fn name_of(name: &OsStr) -> Result<String, Errno> {
    // The server keeps names as text; a name that is not UTF-8 cannot exist there.
    name.to_str().map(str::to_string).ok_or(Errno::EINVAL)
}

impl Filesystem for DriveFs {
    fn init(&mut self, _req: &Request, config: &mut fuser::KernelConfig) -> std::io::Result<()> {
        // Listings with attributes in one answer (a file manager stats every entry), larger
        // reads and writes, parallel folder operations.
        let _ = config.add_capabilities(
            fuser::InitFlags::FUSE_DO_READDIRPLUS
                | fuser::InitFlags::FUSE_READDIRPLUS_AUTO
                | fuser::InitFlags::FUSE_PARALLEL_DIROPS,
        );
        let _ = config.set_max_readahead(1 << 20);
        let _ = config.set_max_write(1 << 20);
        Ok(())
    }

    fn lookup(&self, req: &Request, parent: INodeNo, name: &OsStr, reply: ReplyEntry) {
        let name = match name_of(name) {
            Ok(name) => name,
            Err(_) => return reply.error(Errno::ENOENT),
        };
        let drive = self.drive.clone();
        if let Some(result) = drive.lookup_now(parent.0, &name) {
            return match result {
                Ok(attr) => reply.entry(&TTL, &attr, Generation(0)),
                Err(e) => reply.error(e),
            };
        }
        let pid = req.pid();
        let slot = drive.watchdog.slot(reply, METADATA);
        self.drive.rt.spawn(async move {
            let result = drive.lookup(parent.0, &name, is_scanner(pid)).await;
            if let Some(reply) = slot.take() {
                match result {
                    Ok(attr) => reply.entry(&TTL, &attr, Generation(0)),
                    Err(e) => reply.error(e),
                }
            }
        });
    }

    fn getattr(&self, _req: &Request, ino: INodeNo, _fh: Option<FileHandle>, reply: ReplyAttr) {
        match self.drive.getattr(ino.0) {
            Ok(attr) => reply.attr(&TTL, &attr),
            Err(e) => reply.error(e),
        }
    }

    fn setattr(
        &self,
        _req: &Request,
        ino: INodeNo,
        _mode: Option<u32>,
        _uid: Option<u32>,
        _gid: Option<u32>,
        size: Option<u64>,
        _atime: Option<TimeOrNow>,
        mtime: Option<TimeOrNow>,
        _ctime: Option<SystemTime>,
        fh: Option<FileHandle>,
        _crtime: Option<SystemTime>,
        _chgtime: Option<SystemTime>,
        _bkuptime: Option<SystemTime>,
        _flags: Option<fuser::BsdFileFlags>,
        _kill_suid_gid: bool,
        reply: ReplyAttr,
    ) {
        let mtime = mtime.map(|t| match t {
            TimeOrNow::SpecificTime(t) => t,
            TimeOrNow::Now => SystemTime::now(),
        });
        let drive = self.drive.clone();
        let slot = drive.watchdog.slot(reply, CONTENT);
        self.drive.rt.spawn(async move {
            let result = drive.setattr(ino.0, size, mtime, fh.map(|f| f.0)).await;
            if let Some(reply) = slot.take() {
                match result {
                    Ok(attr) => reply.attr(&TTL, &attr),
                    Err(e) => reply.error(e),
                }
            }
        });
    }

    fn mkdir(&self, _req: &Request, parent: INodeNo, name: &OsStr, _mode: u32, _umask: u32, _owner: fuser::Owner, reply: ReplyEntry) {
        let name = match name_of(name) {
            Ok(name) => name,
            Err(e) => return reply.error(e),
        };
        let drive = self.drive.clone();
        let slot = drive.watchdog.slot(reply, METADATA);
        let check = slot.clone();
        self.drive.rt.spawn(async move {
            let result = drive.mkdir(parent.0, &name, || check.expired()).await;
            if let Some(reply) = slot.take() {
                match result {
                    Ok(attr) => reply.entry(&TTL, &attr, Generation(0)),
                    Err(e) => reply.error(e),
                }
            }
        });
    }

    fn unlink(&self, _req: &Request, parent: INodeNo, name: &OsStr, reply: ReplyEmpty) {
        self.remove(parent, name, false, reply);
    }

    fn rmdir(&self, _req: &Request, parent: INodeNo, name: &OsStr, reply: ReplyEmpty) {
        self.remove(parent, name, true, reply);
    }

    fn symlink(&self, _req: &Request, _parent: INodeNo, _link_name: &OsStr, _target: &std::path::Path, _owner: fuser::Owner, reply: ReplyEntry) {
        reply.error(Errno::ENOTSUP);
    }

    fn link(&self, _req: &Request, _ino: INodeNo, _newparent: INodeNo, _newname: &OsStr, reply: ReplyEntry) {
        reply.error(Errno::ENOTSUP);
    }

    fn mknod(&self, _req: &Request, _parent: INodeNo, _name: &OsStr, _mode: u32, _umask: u32, _rdev: u32, _owner: fuser::Owner, reply: ReplyEntry) {
        reply.error(Errno::ENOTSUP);
    }

    fn rename(
        &self,
        _req: &Request,
        parent: INodeNo,
        name: &OsStr,
        newparent: INodeNo,
        newname: &OsStr,
        flags: RenameFlags,
        _owner: Option<fuser::Owner>,
        reply: ReplyEmpty,
    ) {
        let (name, newname) = match (name_of(name), name_of(newname)) {
            (Ok(a), Ok(b)) => (a, b),
            _ => return reply.error(Errno::EINVAL),
        };
        if flags.contains(RenameFlags::RENAME_EXCHANGE) {
            return reply.error(Errno::EINVAL);
        }
        let no_replace = flags.contains(RenameFlags::RENAME_NOREPLACE);
        let drive = self.drive.clone();
        let slot = drive.watchdog.slot(reply, METADATA);
        let check = slot.clone();
        self.drive.rt.spawn(async move {
            let result = drive
                .rename(parent.0, &name, newparent.0, &newname, no_replace, || check.expired())
                .await;
            if let Some(reply) = slot.take() {
                match result {
                    Ok(()) => reply.ok(),
                    Err(e) => reply.error(e),
                }
            }
        });
    }

    fn open(&self, req: &Request, ino: INodeNo, flags: OpenFlags, _kill_suid_gid: bool, reply: ReplyOpen) {
        let write = !matches!(flags.acc_mode(), OpenAccMode::O_RDONLY);
        match self.drive.open(ino.0, write, is_scanner(req.pid())) {
            Ok((fh, keep_cache)) => {
                let flags = if keep_cache { FopenFlags::FOPEN_KEEP_CACHE } else { FopenFlags::empty() };
                reply.opened(FileHandle(fh), flags)
            }
            Err(e) => reply.error(e),
        }
    }

    fn read(
        &self,
        _req: &Request,
        ino: INodeNo,
        fh: FileHandle,
        offset: u64,
        size: u32,
        _flags: OpenFlags,
        _lock_owner: Option<LockOwner>,
        reply: ReplyData,
    ) {
        let (_, cell, _) = match self.drive.handle_cell(fh.0) {
            Ok(handle) => handle,
            Err(e) => return reply.error(e),
        };
        if !self.drive.read_needs(&cell, offset, size) {
            self.drive.read_ahead(ino.0, &cell, offset + size as u64);
            return match cell.read_at(offset, size) {
                Ok(data) => reply.data(&data),
                Err(e) => reply.error(Errno::from(e)),
            };
        }
        let drive = self.drive.clone();
        let slot = drive.watchdog.slot(reply, CONTENT);
        self.drive.rt.spawn(async move {
            let result = drive.read(ino.0, cell, offset, size).await;
            if let Some(reply) = slot.take() {
                match result {
                    Ok(data) => reply.data(&data),
                    Err(e) => reply.error(e),
                }
            }
        });
    }

    fn write(
        &self,
        _req: &Request,
        ino: INodeNo,
        fh: FileHandle,
        offset: u64,
        data: &[u8],
        _write_flags: WriteFlags,
        _flags: OpenFlags,
        _lock_owner: Option<LockOwner>,
        reply: ReplyWrite,
    ) {
        let (_, cell, write) = match self.drive.handle_cell(fh.0) {
            Ok(handle) => handle,
            Err(e) => return reply.error(e),
        };
        if !write {
            return reply.error(Errno::EBADF);
        }
        if !self.drive.write_needs_work(ino.0, &cell, offset, data.len() as u64) {
            return match self.drive.write_now(ino.0, &cell, offset, data) {
                Ok(n) => reply.written(n),
                Err(e) => reply.error(e),
            };
        }
        let data = data.to_vec();
        let drive = self.drive.clone();
        let slot = drive.watchdog.slot(reply, CONTENT);
        self.drive.rt.spawn(async move {
            let result = drive.write(ino.0, cell, offset, data).await;
            if let Some(reply) = slot.take() {
                match result {
                    Ok(n) => reply.written(n),
                    Err(e) => reply.error(e),
                }
            }
        });
    }

    fn flush(&self, _req: &Request, _ino: INodeNo, _fh: FileHandle, _lock_owner: LockOwner, reply: ReplyEmpty) {
        // Every write is in the local file already.
        reply.ok();
    }

    fn release(
        &self,
        _req: &Request,
        _ino: INodeNo,
        fh: FileHandle,
        _flags: OpenFlags,
        _lock_owner: Option<LockOwner>,
        _flush: bool,
        reply: ReplyEmpty,
    ) {
        reply.ok();
        // Records the change (SQLite): not on a FUSE thread.
        let drive = self.drive.clone();
        self.drive.rt.spawn_blocking({
            let drive = drive.clone();
            move || drive.release(fh.0)
        });
    }

    fn fsync(&self, _req: &Request, ino: INodeNo, fh: FileHandle, _datasync: bool, reply: ReplyEmpty) {
        let drive = self.drive.clone();
        let slot = drive.watchdog.slot(reply, CONTENT);
        self.drive.rt.spawn_blocking(move || {
            let result = drive.fsync(ino.0, fh.0);
            if let Some(reply) = slot.take() {
                match result {
                    Ok(()) => reply.ok(),
                    Err(e) => reply.error(e),
                }
            }
        });
    }

    fn readdir(&self, req: &Request, ino: INodeNo, _fh: FileHandle, offset: u64, mut reply: ReplyDirectory) {
        let drive = self.drive.clone();
        let pid = req.pid();
        // A listing in memory is answered at once (refreshed in the background when old).
        if let Some(entries) = drive.readdir_now(ino.0) {
            fill(&mut reply, ino.0, &entries, offset);
            return reply.ok();
        }
        let slot = drive.watchdog.slot(reply, METADATA);
        self.drive.rt.spawn(async move {
            let result = drive.readdir(ino.0, is_scanner(pid)).await;
            if let Some(mut reply) = slot.take() {
                match result {
                    Ok(entries) => {
                        fill(&mut reply, ino.0, &entries, offset);
                        reply.ok();
                    }
                    Err(e) => reply.error(e),
                }
            }
        });
    }

    fn readdirplus(&self, req: &Request, ino: INodeNo, _fh: FileHandle, offset: u64, mut reply: fuser::ReplyDirectoryPlus) {
        let drive = self.drive.clone();
        if drive.listed_now(ino.0) {
            fill_plus(&drive, &mut reply, ino.0, offset);
            return reply.ok();
        }
        let pid = req.pid();
        let slot = drive.watchdog.slot(reply, METADATA);
        self.drive.rt.spawn(async move {
            let result = drive.ensure_listing(ino.0, is_scanner(pid)).await;
            if let Some(mut reply) = slot.take() {
                match result {
                    Ok(()) => {
                        fill_plus(&drive, &mut reply, ino.0, offset);
                        reply.ok();
                    }
                    Err(e) => reply.error(e),
                }
            }
        });
    }

    fn statfs(&self, _req: &Request, _ino: INodeNo, reply: ReplyStatfs) {
        let (total, free) = self.drive.statfs();
        const BLOCK: u64 = 4096;
        reply.statfs(total / BLOCK, free / BLOCK, free / BLOCK, 0, 0, BLOCK as u32, 255, BLOCK as u32);
    }

    fn create(
        &self,
        _req: &Request,
        parent: INodeNo,
        name: &OsStr,
        _mode: u32,
        _umask: u32,
        _flags: i32,
        _kill_suid_gid: bool,
        _owner: fuser::Owner,
        reply: ReplyCreate,
    ) {
        let name = match name_of(name) {
            Ok(name) => name,
            Err(e) => return reply.error(e),
        };
        let drive = self.drive.clone();
        let slot = drive.watchdog.slot(reply, METADATA);
        self.drive.rt.spawn(async move {
            let result = drive.create(parent.0, &name).await;
            match (slot.take(), result) {
                (Some(reply), Ok((attr, fh))) => {
                    reply.created(&TTL, &attr, Generation(0), FileHandle(fh), FopenFlags::empty())
                }
                (Some(reply), Err(e)) => reply.error(e),
                // Too late: the program got an error, the new file is closed again.
                (None, Ok((_, fh))) => drive.release(fh),
                (None, Err(_)) => {}
            }
        });
    }

    fn getxattr(&self, _req: &Request, _ino: INodeNo, _name: &OsStr, _size: u32, reply: fuser::ReplyXattr) {
        reply.error(Errno::ENOTSUP);
    }

    fn listxattr(&self, _req: &Request, _ino: INodeNo, size: u32, reply: fuser::ReplyXattr) {
        if size == 0 {
            reply.size(0);
        } else {
            reply.data(&[]);
        }
    }

    fn setxattr(&self, _req: &Request, _ino: INodeNo, _name: &OsStr, _value: &[u8], _flags: i32, _position: u32, reply: ReplyEmpty) {
        reply.error(Errno::ENOTSUP);
    }

    fn removexattr(&self, _req: &Request, _ino: INodeNo, _name: &OsStr, reply: ReplyEmpty) {
        reply.error(Errno::ENOTSUP);
    }
}

impl DriveFs {
    fn remove(&self, parent: INodeNo, name: &OsStr, dir: bool, reply: ReplyEmpty) {
        let name = match name_of(name) {
            Ok(name) => name,
            Err(_) => return reply.error(Errno::ENOENT),
        };
        let drive = self.drive.clone();
        let slot = drive.watchdog.slot(reply, METADATA);
        let check = slot.clone();
        self.drive.rt.spawn(async move {
            let result = drive.unlink(parent.0, &name, dir, || check.expired()).await;
            if let Some(reply) = slot.take() {
                match result {
                    Ok(()) => reply.ok(),
                    Err(e) => reply.error(e),
                }
            }
        });
    }
}

/// Add entries with attributes from `offset` on ("." and ".." first).
fn fill_plus(drive: &Drive, reply: &mut fuser::ReplyDirectoryPlus, ino: u64, offset: u64) {
    let Ok(own) = drive.getattr(ino) else { return };
    let mut all = vec![(".".to_string(), own), ("..".to_string(), own)];
    all.extend(drive.entries_plus(ino));
    for (i, (name, attr)) in all.into_iter().enumerate().skip(offset as usize) {
        if reply.add(attr.ino, (i + 1) as u64, name, &TTL, &attr, Generation(0)) {
            break;
        }
    }
}

/// Add entries from `offset` on ("." and ".." first).
fn fill(reply: &mut ReplyDirectory, ino: u64, entries: &[(u64, fuser::FileType, String)], offset: u64) {
    let mut all = vec![(ino, fuser::FileType::Directory, ".".to_string()), (ino, fuser::FileType::Directory, "..".to_string())];
    all.extend(entries.iter().cloned());
    for (i, (child, kind, name)) in all.into_iter().enumerate().skip(offset as usize) {
        if reply.add(INodeNo(child), (i + 1) as u64, kind, name) {
            break;
        }
    }
}
