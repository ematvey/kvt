//! kvt-fuse — FUSE filesystem for KVT vault.
//!
//! Mounts a KVT vault as a native Linux filesystem at a user-specified mountpoint.
//! Every file write through the mount triggers a KVT REST API call and produces a
//! git commit on the daemon side.
//!
//! # Architecture
//!
//! - FUSE callbacks are synchronous (libfuse contract). Each one bridges into the
//!   async HTTP client via `Handle::block_on`.
//! - Open-to-close consistency: on `open()`, the file content is fetched from KVT
//!   into a local scratch file. `write()` accumulates bytes there. On `flush()` /
//!   `release()`, if dirty, the content is pushed back to KVT via POST/PATCH.
//! - The scratch directory is cleaned up on unmount.

pub mod fs;