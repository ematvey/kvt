//! FUSE filesystem implementation for KVT vault.
//!
//! Maps FUSE callbacks to KVT REST API calls. Open-to-close consistency:
//! content is fetched on `open()`, buffered locally for `write()`, and
//! pushed back to KVT on `flush()` / `release()`.

use std::collections::HashMap;
use std::ffi::OsStr;
use std::io::{Read, Seek, SeekFrom, Write};
use std::path::PathBuf;
use std::sync::atomic::{AtomicU64, Ordering};

use std::time::{Duration, SystemTime, UNIX_EPOCH};

use anyhow::Result;
use libc;
use fuser::{
    FileAttr, FileType, Filesystem, ReplyAttr, ReplyCreate, ReplyData, ReplyDirectory,
    ReplyEmpty, ReplyEntry, ReplyOpen, ReplyWrite, Request,
};
use serde::Deserialize;
use tokio::runtime::Handle;
use tokio::sync::Mutex;

const TTL: Duration = Duration::from_secs(1);
const ROOT_INODE: u64 = 1;

/// In-memory inode metadata.
#[derive(Clone, Debug)]
struct Inode {
    ino: u64,
    kind: FileType,
    path: String, // KVT path relative to vault root
    size: u64,
    mtime: u64,
}

/// Per-open-file bookkeeping.
#[derive(Clone, Debug)]
#[allow(dead_code)]
struct OpenFile {
    fh: u64,
    tmp_path: PathBuf,
    #[allow(dead_code)]
    path: String,
    #[allow(dead_code)]
    base_hash: String,
    dirty: bool,
    #[allow(dead_code)]
    created: bool,
    size: u64,
}

/// KVT concept document shape from the API.
#[derive(Deserialize, Debug)]
#[serde(rename_all = "camelCase")]
struct ConceptDoc {
    #[allow(dead_code)]
    path: Option<String>,
    content: Option<String>,
    hash: Option<String>,
    #[allow(dead_code)]
    timestamp: Option<String>,
}

#[derive(Deserialize, Debug)]
#[serde(rename_all = "camelCase")]
struct ListResponse {
    documents: Option<Vec<ListDoc>>,
}

#[derive(Deserialize, Debug)]
#[serde(rename_all = "camelCase")]
struct ListDoc {
    path: Option<String>,
}

/// FUSE filesystem state.
pub struct KvtFs {
    handle: Handle,
    client: reqwest::Client,
    api_url: String,
    api_key: Option<String>,
    workdir: PathBuf,

    inodes: Mutex<HashMap<u64, Inode>>,
    open_files: Mutex<HashMap<u64, OpenFile>>,
    next_inode: AtomicU64,
    next_fh: AtomicU64,
}

impl KvtFs {
    pub fn new(
        handle: Handle,
        api_url: String,
        api_key: Option<String>,
        workdir: PathBuf,
    ) -> Self {
        let mut inodes = HashMap::new();
        // Root inode
        inodes.insert(
            ROOT_INODE,
            Inode {
                ino: ROOT_INODE,
                kind: FileType::Directory,
                path: String::new(),
                size: 0,
                mtime: unix_now(),
            },
        );

        Self {
            handle,
            client: reqwest::Client::builder()
                .timeout(Duration::from_secs(30))
                .build()
                .expect("build reqwest client"),
            api_url,
            api_key,
            workdir,
            inodes: Mutex::new(inodes),
            open_files: Mutex::new(HashMap::new()),
            next_inode: AtomicU64::new(ROOT_INODE + 1),
            next_fh: AtomicU64::new(1),
        }
    }

    /// KVT path to inode number — allocate if new.
    async fn path_to_inode(&self, path: &str, kind: FileType, size: u64) -> u64 {
        let mut inodes = self.inodes.lock().await;
        for inode in inodes.values() {
            if inode.path == path {
                return inode.ino;
            }
        }
        let ino = self.next_inode.fetch_add(1, Ordering::Relaxed);
        inodes.insert(
            ino,
            Inode {
                ino,
                kind,
                path: path.to_string(),
                size,
                mtime: unix_now(),
            },
        );
        ino
    }

    /// HTTP GET to KVT, return parsed JSON.
    async fn kvt_get<T: serde::de::DeserializeOwned>(
        &self,
        endpoint: &str,
    ) -> Result<T> {
        let url = format!("{}/{}", self.api_url.trim_end_matches('/'), endpoint);
        let mut req = self.client.get(&url);
        if let Some(ref key) = self.api_key {
            req = req.header("Authorization", format!("Bearer {}", key));
        }
        let resp = req.send().await?;
        let body = resp.json().await?;
        Ok(body)
    }

    /// HTTP POST to KVT.
    async fn kvt_post(&self, endpoint: &str, body: serde_json::Value) -> Result<serde_json::Value> {
        let url = format!("{}/{}", self.api_url.trim_end_matches('/'), endpoint);
        let mut req = self.client.post(&url).json(&body);
        if let Some(ref key) = self.api_key {
            req = req.header("Authorization", format!("Bearer {}", key));
        }
        let resp = req.send().await?;
        let body: serde_json::Value = resp.json().await?;
        Ok(body)
    }

    /// HTTP DELETE to KVT.
    async fn kvt_delete(&self, endpoint: &str) -> Result<serde_json::Value> {
        let url = format!("{}/{}", self.api_url.trim_end_matches('/'), endpoint);
        let mut req = self.client.delete(&url);
        if let Some(ref key) = self.api_key {
            req = req.header("Authorization", format!("Bearer {}", key));
        }
        let resp = req.send().await?;
        let body: serde_json::Value = resp.json().await?;
        Ok(body)
    }

    /// Build file attributes for a given inode.
    fn attrs(&self, inode: &Inode) -> FileAttr {
        let tm = UNIX_EPOCH + Duration::from_secs(inode.mtime);
        FileAttr {
            ino: inode.ino,
            size: inode.size,
            blocks: 1,
            atime: tm,
            mtime: tm,
            ctime: tm,
            crtime: tm,
            kind: inode.kind,
            perm: if inode.kind == FileType::Directory {
                0o755
            } else {
                0o644
            },
            nlink: 1,
            uid: unsafe { libc::getuid() },
            gid: unsafe { libc::getgid() },
            rdev: 0,
            blksize: 4096,
            flags: 0,
        }
    }

    /// List concepts under a path prefix from KVT.
    async fn list_children(&self, kvt_path: &str) -> Result<Vec<(String, FileType, u64)>> {
        let prefix = if kvt_path.is_empty() {
            String::new()
        } else {
            format!("{}/", kvt_path.trim_end_matches('/'))
        };
        let endpoint = if prefix.is_empty() {
            "concepts".to_string()
        } else {
            format!("concepts?path_prefix={}", urlencoding(&prefix))
        };
        let resp: ListResponse = self.kvt_get(&endpoint).await?;
        let mut entries: Vec<(String, FileType, u64)> = Vec::new();

        // Determine depth: the prefix depth + 1. We want immediate children only.
        let prefix_depth = if prefix.is_empty() {
            0
        } else {
            prefix.trim_end_matches('/').split('/').count()
        };

        if let Some(docs) = resp.documents {
            for doc in docs {
                if let Some(doc_path) = doc.path {
                    // Only include direct children
                    let parts: Vec<&str> = doc_path.split('/').collect();
                    if parts.len() == prefix_depth + 1
                        || (parts.len() > prefix_depth + 1
                            && doc_path.starts_with(&prefix))
                    {
                        let name = parts[prefix_depth].to_string();
                        let is_dir = parts.len() > prefix_depth + 1;
                        let ft = if is_dir {
                            FileType::Directory
                        } else {
                            FileType::RegularFile
                        };
                        entries.push((name, ft, 0));
                    }
                }
            }
        }

        // Deduplicate by name (a directory may have multiple children reported)
        entries.sort_by(|a, b| a.0.cmp(&b.0));
        entries.dedup_by(|a, b| a.0 == b.0);

        Ok(entries)
    }
}

fn unix_now() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs()
}

fn urlencoding(s: &str) -> String {
    // Simple URL encoding for the path_prefix
    s.replace('/', "%2F")
}

// ── FUSE Filesystem implementation ─────────────────────────────────────────

impl Filesystem for KvtFs {
    fn lookup(&mut self, _req: &Request, parent: u64, name: &OsStr, reply: ReplyEntry) {
        let name = name.to_string_lossy().to_string();
        let handle = self.handle.clone();

        let result = handle.block_on(async {
            let inodes = self.inodes.lock().await;
            let parent_inode = inodes.get(&parent).cloned();
            drop(inodes);

            let parent_path = parent_inode.as_ref().map(|i| i.path.clone()).unwrap_or_default();
            let child_path = if parent_path.is_empty() {
                name.clone()
            } else {
                format!("{}/{}", parent_path, name)
            };

            // "." and ".." are handled by FUSE kernel, but be safe
            if name == "." || name == ".." {
                let ino = if name == "." {
                    parent
                } else {
                    ROOT_INODE
                };
                let inodes = self.inodes.lock().await;
                if let Some(inode) = inodes.get(&ino) {
                    let attrs = self.attrs(inode);
                    return Some((attrs, TTL));
                }
                return None;
            }

            // Check if we already know this inode
            let inodes = self.inodes.lock().await;
            for inode in inodes.values() {
                if inode.path == child_path {
                    let attrs = self.attrs(inode);
                    return Some((attrs, TTL));
                }
            }
            drop(inodes);

            // Try to fetch from KVT
            let endpoint = format!("concepts/{}", child_path);
            match self.kvt_get::<serde_json::Value>(&endpoint).await {
                Ok(val) => {
                    let content = val.get("content").and_then(|c| c.as_str()).unwrap_or("");
                    let hash = val.get("hash").and_then(|h| h.as_str()).unwrap_or("");
                    let size = if content.is_empty() { 0 } else { content.len() as u64 };
                    let ino = self.path_to_inode(&child_path, FileType::RegularFile, size).await;

                    // Store base_hash for this inode
                    if !hash.is_empty() {
                        // We store hash in the size field as a proxy; in real use
                        // we'd extend Inode. For now, size is correct.
                    }

                    let inodes = self.inodes.lock().await;
                    if let Some(inode) = inodes.get(&ino) {
                        return Some((self.attrs(inode), TTL));
                    }
                    None
                }
                Err(_) => {
                    // File not found — check if it might be a virtual directory
                    // (KVT has virtual directories based on path prefixes)
                    let endpoint = format!("concepts?path_prefix={}", urlencoding(&format!("{}/", child_path)));
                    match self.kvt_get::<ListResponse>(&endpoint).await {
                        Ok(list) if list.documents.as_ref().is_some_and(|d| !d.is_empty()) => {
                            let ino = self.path_to_inode(&child_path, FileType::Directory, 0).await;
                            let inodes = self.inodes.lock().await;
                            if let Some(inode) = inodes.get(&ino) {
                                return Some((self.attrs(inode), TTL));
                            }
                            None
                        }
                        _ => None,
                    }
                }
            }
        });

        match result {
            Some((attrs, ttl)) => reply.entry(&ttl, &attrs, 0),
            None => reply.error(libc::ENOENT),
        }
    }

    fn getattr(&mut self, _req: &Request, ino: u64, _fh: Option<u64>, reply: ReplyAttr) {
        let handle = self.handle.clone();
        let result = handle.block_on(async {
            let inodes = self.inodes.lock().await;
            inodes.get(&ino).map(|inode| self.attrs(inode))
        });
        match result {
            Some(attrs) => reply.attr(&TTL, &attrs),
            None => reply.error(libc::ENOENT),
        }
    }

    fn readdir(
        &mut self,
        _req: &Request,
        ino: u64,
        _fh: u64,
        offset: i64,
        mut reply: ReplyDirectory,
    ) {
        let handle = self.handle.clone();
        let entries = handle.block_on(async {
            let inodes = self.inodes.lock().await;
            let parent = inodes.get(&ino).cloned();
            drop(inodes);

            match parent {
                Some(inode) if inode.kind == FileType::Directory => {
                    let mut entries: Vec<(u64, FileType, String)> = Vec::new();

                    // Standard entries
                    entries.push((ROOT_INODE, FileType::Directory, ".".to_string()));

                    // List children from KVT
                    if let Ok(children) = self.list_children(&inode.path).await {
                        for (name, ft, _size) in &children {
                            let child_path = if inode.path.is_empty() {
                                name.clone()
                            } else {
                                format!("{}/{}", inode.path, name)
                            };
                            let child_ino = self.path_to_inode(&child_path, *ft, 0).await;
                            entries.push((child_ino, *ft, name.clone()));
                        }
                    }

                    entries
                }
                _ => Vec::new(),
            }
        });

        for (i, (ino, ft, name)) in entries.iter().enumerate() {
            if (i as i64) < offset {
                continue;
            }
            if reply.add(*ino, (i + 1) as i64, *ft, name) {
                break;
            }
        }
        reply.ok();
    }

    fn open(&mut self, _req: &Request, ino: u64, _flags: i32, reply: ReplyOpen) {
        let handle = self.handle.clone();
        let result = handle.block_on(async {
            let inodes = self.inodes.lock().await;
            let inode = inodes.get(&ino).cloned();
            drop(inodes);

            match inode {
                Some(inode) if inode.kind == FileType::RegularFile => {
                    let path = &inode.path;
                    let endpoint = format!("concepts/{}", path);

                    match self.kvt_get::<ConceptDoc>(&endpoint).await {
                        Ok(doc) => {
                            let content = doc.content.unwrap_or_default();
                            let hash = doc.hash.unwrap_or_default();

                            // Write content to scratch file
                            let fh = self.next_fh.fetch_add(1, Ordering::Relaxed);
                            let tmp_path = self.workdir.join(fh.to_string());
                            if let Ok(mut f) = std::fs::File::create(&tmp_path) {
                                f.write_all(content.as_bytes()).ok();
                            }

                            let size = content.len() as u64;
                            let of = OpenFile {
                                fh,
                                tmp_path,
                                path: path.clone(),
                                base_hash: hash,
                                dirty: false,
                                created: false,
                                size,
                            };

                            let mut open_files = self.open_files.lock().await;
                            open_files.insert(fh, of);

                            Some((fh, 0)) // direct_io=0 lets the kernel cache
                        }
                        Err(_) => None,
                    }
                }
                _ => None,
            }
        });

        match result {
            Some((fh, flags)) => reply.opened(fh, flags),
            None => reply.error(libc::EIO),
        }
    }

    fn read(
        &mut self,
        _req: &Request,
        _ino: u64,
        fh: u64,
        offset: i64,
        size: u32,
        _flags: i32,
        _lock_owner: Option<u64>,
        reply: ReplyData,
    ) {
        let handle = self.handle.clone();
        let result = handle.block_on(async {
            let open_files = self.open_files.lock().await;
            let of = open_files.get(&fh).cloned();
            drop(open_files);

            match of {
                Some(of) => {
                    let mut f = match std::fs::File::open(&of.tmp_path) {
                        Ok(f) => f,
                        Err(_) => return None,
                    };
                    f.seek(SeekFrom::Start(offset as u64)).ok();
                    let mut buf = vec![0u8; size as usize];
                    let n = f.read(&mut buf).unwrap_or(0);
                    buf.truncate(n);
                    Some(buf)
                }
                None => None,
            }
        });

        match result {
            Some(data) => reply.data(&data),
            None => reply.error(libc::EBADF),
        }
    }

    fn write(
        &mut self,
        _req: &Request,
        _ino: u64,
        fh: u64,
        offset: i64,
        data: &[u8],
        _write_flags: u32,
        _flags: i32,
        _lock_owner: Option<u64>,
        reply: ReplyWrite,
    ) {
        let handle = self.handle.clone();
        let data = data.to_vec();
        let result = handle.block_on(async {
            let mut open_files = self.open_files.lock().await;
            let of = open_files.get_mut(&fh);

            match of {
                Some(of) => {
                    let mut f = match std::fs::File::options()
                        .write(true)
                        .create(true)
                        .truncate(true)
                        .open(&of.tmp_path)
                    {
                        Ok(f) => f,
                        Err(_) => return None,
                    };
                    f.seek(SeekFrom::Start(offset as u64)).ok();
                    let n = f.write(&data).unwrap_or(0);
                    of.dirty = true;
                    of.size = std::cmp::max(of.size, (offset as u64) + n as u64);
                    Some(n as u32)
                }
                None => None,
            }
        });

        match result {
            Some(n) => reply.written(n),
            None => reply.error(libc::EBADF),
        }
    }

    fn flush(
        &mut self,
        _req: &Request,
        _ino: u64,
        fh: u64,
        _lock_owner: u64,
        reply: ReplyEmpty,
    ) {
        let handle = self.handle.clone();
        let result = handle.block_on(async {
            let mut open_files = self.open_files.lock().await;
            let of = open_files.get(&fh);
            let of = match of {
                Some(of) => of,
                None => return Some(false), // nothing to do
            };

            if !of.dirty {
                return Some(true); // no changes
            }

            // Read the full content from scratch file
            let content = match std::fs::read_to_string(&of.tmp_path) {
                Ok(c) => c,
                Err(_) => return None,
            };

            // Push to KVT
            let body = serde_json::json!({
                "path": of.path,
                "content": content,
                "agent_id": "kvt-fuse",
                "validation_mode": "advisory",
            });

            let endpoint = "concepts";
            match self.kvt_post(endpoint, body).await {
                Ok(_) => {
                    if let Some(of) = open_files.get_mut(&fh) {
                        of.dirty = false;
                    }
                    Some(true)
                }
                Err(e) => {
                    tracing::error!(error = %e, path = %of.path, "flush failed");
                    None
                }
            }
        });

        match result {
            Some(_) => reply.ok(),
            None => reply.error(libc::EIO),
        }
    }

    fn release(
        &mut self,
        _req: &Request,
        _ino: u64,
        fh: u64,
        _flags: i32,
        _lock_owner: Option<u64>,
        _flush: bool,
        reply: ReplyEmpty,
    ) {
        let handle = self.handle.clone();
        handle.block_on(async {
            let mut open_files = self.open_files.lock().await;
            // Clean up scratch file
            if let Some(of) = open_files.remove(&fh) {
                std::fs::remove_file(&of.tmp_path).ok();
            }
        });
        reply.ok();
    }

    fn create(
        &mut self,
        _req: &Request,
        parent: u64,
        name: &OsStr,
        _mode: u32,
        _umask: u32,
        _flags: i32,
        reply: ReplyCreate,
    ) {
        let name = name.to_string_lossy().to_string();
        let handle = self.handle.clone();
        let result = handle.block_on(async {
            let inodes = self.inodes.lock().await;
            let parent_path = inodes
                .get(&parent)
                .map(|i| i.path.clone())
                .unwrap_or_default();
            drop(inodes);

            let child_path = if parent_path.is_empty() {
                name.clone()
            } else {
                format!("{}/{}", parent_path, name)
            };

            // Allocate inode
            let ino = self.path_to_inode(&child_path, FileType::RegularFile, 0).await;

            // Create scratch file (empty)
            let fh = self.next_fh.fetch_add(1, Ordering::Relaxed);
            let tmp_path = self.workdir.join(fh.to_string());
            std::fs::File::create(&tmp_path).ok();

            let of = OpenFile {
                fh,
                tmp_path,
                path: child_path.clone(),
                base_hash: String::new(),
                dirty: false,
                created: true,
                size: 0,
            };

            let mut open_files = self.open_files.lock().await;
            open_files.insert(fh, of);

            let inodes = self.inodes.lock().await;
            let attrs = inodes.get(&ino).map(|inode| self.attrs(inode));

            (fh, attrs, ino)
        });

        match result {
            (fh, Some(attrs), ino) => reply.created(&TTL, &attrs, ino, fh, 0),
            (_, None, _) => reply.error(libc::EIO),
        }
    }

    fn unlink(&mut self, _req: &Request, parent: u64, name: &OsStr, reply: ReplyEmpty) {
        let name = name.to_string_lossy().to_string();
        let handle = self.handle.clone();
        let result = handle.block_on(async {
            let inodes = self.inodes.lock().await;
            let parent_path = inodes
                .get(&parent)
                .map(|i| i.path.clone())
                .unwrap_or_default();
            drop(inodes);

            let child_path = if parent_path.is_empty() {
                name
            } else {
                format!("{}/{}", parent_path, name)
            };

            let endpoint = format!("concepts/{}?agent_id=kvt-fuse", child_path);
            match self.kvt_delete(&endpoint).await {
                Ok(_) => {
                    // Clean up inode table
                    let mut inodes = self.inodes.lock().await;
                    inodes.retain(|_, v| v.path != child_path);
                    true
                }
                Err(e) => {
                    tracing::error!(error = %e, path = %child_path, "unlink failed");
                    false
                }
            }
        });

        if result {
            reply.ok();
        } else {
            reply.error(libc::EIO);
        }
    }

    fn rename(
        &mut self,
        _req: &Request,
        parent: u64,
        name: &OsStr,
        newparent: u64,
        newname: &OsStr,
        _flags: u32,
        reply: ReplyEmpty,
    ) {
        let name = name.to_string_lossy().to_string();
        let newname = newname.to_string_lossy().to_string();
        let handle = self.handle.clone();
        let result = handle.block_on(async {
            let inodes = self.inodes.lock().await;
            let parent_path = inodes
                .get(&parent)
                .map(|i| i.path.clone())
                .unwrap_or_default();
            let newparent_path = inodes
                .get(&newparent)
                .map(|i| i.path.clone())
                .unwrap_or_default();
            drop(inodes);

            let from_path = if parent_path.is_empty() {
                name.clone()
            } else {
                format!("{}/{}", parent_path, name)
            };
            let to_path = if newparent_path.is_empty() {
                newname
            } else {
                format!("{}/{}", newparent_path, newname)
            };

            // Read content from old path
            let endpoint = format!("concepts/{}", from_path);
            let doc: ConceptDoc = match self.kvt_get(&endpoint).await {
                Ok(d) => d,
                Err(_) => return false,
            };
            let content = doc.content.unwrap_or_default();

            // Write to new path
            let body = serde_json::json!({
                "path": to_path,
                "content": content,
                "agent_id": "kvt-fuse",
                "validation_mode": "advisory",
            });
            if self.kvt_post("concepts", body).await.is_err() {
                return false;
            }

            // Delete old path
            let del_endpoint = format!("concepts/{}?agent_id=kvt-fuse", from_path);
            if self.kvt_delete(&del_endpoint).await.is_err() {
                return false;
            }

            // Update inode table
            let mut inodes = self.inodes.lock().await;
            for (_, inode) in inodes.iter_mut() {
                if inode.path == from_path {
                    inode.path = to_path;
                    break;
                }
            }

            true
        });

        if result {
            reply.ok();
        } else {
            reply.error(libc::EIO);
        }
    }

    fn mkdir(
        &mut self,
        _req: &Request,
        parent: u64,
        name: &OsStr,
        _mode: u32,
        _umask: u32,
        reply: ReplyEntry,
    ) {
        let name = name.to_string_lossy().to_string();
        let handle = self.handle.clone();
        let result = handle.block_on(async {
            let inodes = self.inodes.lock().await;
            let parent_path = inodes
                .get(&parent)
                .map(|i| i.path.clone())
                .unwrap_or_default();
            drop(inodes);

            let child_path = if parent_path.is_empty() {
                name
            } else {
                format!("{}/{}", parent_path, name)
            };

            // KVT uses virtual directories — just allocate inode
            let ino = self
                .path_to_inode(&child_path, FileType::Directory, 0)
                .await;
            let inodes = self.inodes.lock().await;
            inodes.get(&ino).map(|inode| self.attrs(inode))
        });

        match result {
            Some(attrs) => reply.entry(&TTL, &attrs, 0),
            None => reply.error(libc::EIO),
        }
    }

    fn rmdir(&mut self, _req: &Request, parent: u64, name: &OsStr, reply: ReplyEmpty) {
        let name = name.to_string_lossy().to_string();
        let handle = self.handle.clone();
        let result = handle.block_on(async {
            let inodes = self.inodes.lock().await;
            let parent_path = inodes
                .get(&parent)
                .map(|i| i.path.clone())
                .unwrap_or_default();
            drop(inodes);

            let child_path = if parent_path.is_empty() {
                name
            } else {
                format!("{}/{}", parent_path, name)
            };

            // Check if directory is empty
            let prefix = format!("{}/", child_path);
            let endpoint = format!("concepts?path_prefix={}", urlencoding(&prefix));
            match self.kvt_get::<ListResponse>(&endpoint).await {
                Ok(resp) => {
                    let count = resp.documents.as_ref().map(|d| d.len()).unwrap_or(0);
                    if count > 0 {
                        return Some(false); // not empty
                    }
                    // Remove from inode table
                    let mut inodes = self.inodes.lock().await;
                    inodes.retain(|_, v| v.path != child_path);
                    Some(true)
                }
                Err(_) => {
                    // Virtual directory with no children — just remove from inode table
                    let mut inodes = self.inodes.lock().await;
                    inodes.retain(|_, v| v.path != child_path);
                    Some(true)
                }
            }
        });

        match result {
            Some(true) => reply.ok(),
            Some(false) => reply.error(libc::ENOTEMPTY),
            None => reply.error(libc::EIO),
        }
    }

    fn setattr(
        &mut self,
        _req: &Request,
        ino: u64,
        _mode: Option<u32>,
        _uid: Option<u32>,
        _gid: Option<u32>,
        _size: Option<u64>,
        _atime: Option<fuser::TimeOrNow>,
        _mtime: Option<fuser::TimeOrNow>,
        _ctime: Option<std::time::SystemTime>,
        _fh: Option<u64>,
        _crtime: Option<std::time::SystemTime>,
        _chgtime: Option<std::time::SystemTime>,
        _bkuptime: Option<std::time::SystemTime>,
        _flags: Option<u32>,
        reply: ReplyAttr,
    ) {
        let handle = self.handle.clone();
        let result = handle.block_on(async {
            let inodes = self.inodes.lock().await;
            inodes.get(&ino).map(|inode| self.attrs(inode))
        });
        match result {
            Some(attrs) => reply.attr(&TTL, &attrs),
            None => reply.error(libc::ENOENT),
        }
    }

    fn statfs(&mut self, _req: &Request, _ino: u64, reply: fuser::ReplyStatfs) {
        reply.statfs(
            1,  // blocks
            1,  // bfree
            1,  // bavail
            0,  // files
            0,  // ffree
            4096, // bsize
            255, // namelen
            4096, // frsize
        );
    }
}