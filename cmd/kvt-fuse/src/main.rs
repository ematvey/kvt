//! CLI entry point for kvt-fuse.
//!
//! Builds a tokio multi-thread runtime, instantiates the FUSE filesystem with a
//! KVT HTTP client, and mounts via `fuser::spawn_mount2`. The main thread waits
//! for a SIGTERM/SIGINT to cleanly unmount.

use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, Ordering};

use anyhow::{Context, Result};
use clap::Parser;

use kvt_fuse::fs::KvtFs;

static SHUTDOWN: AtomicBool = AtomicBool::new(false);

#[derive(Debug, Parser)]
#[command(name = "kvt-fuse", about = "FUSE filesystem for KVT vault")]
struct Args {
    /// Directory to mount the vault at.
    #[arg(long, required = true)]
    mountpoint: PathBuf,

    /// KVT HTTP API base URL.
    #[arg(long, default_value = "http://localhost:8200")]
    api_url: String,

    /// Optional bearer token for KVT auth.
    #[arg(long)]
    api_key: Option<String>,

    /// Log file path.
    #[arg(long, default_value = "")]
    log_file: Option<PathBuf>,

    /// Allow other users to access the mount.
    #[arg(long, default_value_t = false)]
    allow_other: bool,
}

fn main() -> Result<()> {
    let args = Args::parse();

    // Init logging
    let log_file = args
        .log_file
        .or_else(|| {
            dirs_or_default().map(|mut p| {
                p.push("fuse.log");
                p
            })
        })
        .or_else(|| Some(PathBuf::from("/tmp/kvt-fuse.log")));
    if let Some(ref path) = log_file {
        if let Some(parent) = path.parent() {
            std::fs::create_dir_all(parent).ok();
        }
    }

    let log_writer: Box<dyn std::io::Write + Send + Sync> = if let Some(ref path) = log_file {
        match std::fs::File::create(path) {
            Ok(f) => Box::new(f),
            Err(_) => Box::new(std::io::stderr()),
        }
    } else {
        Box::new(std::io::stderr())
    };

    tracing_subscriber::fmt()
        .with_writer(std::sync::Mutex::new(log_writer))
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| "info".into()),
        )
        .init();

    tracing::info!(mountpoint = %args.mountpoint.display(), api_url = %args.api_url, "starting kvt-fuse");

    // Ensure mountpoint exists
    std::fs::create_dir_all(&args.mountpoint)
        .with_context(|| format!("create mountpoint {}", args.mountpoint.display()))?;

    // Build a multi-thread tokio runtime for HTTP calls
    let runtime = tokio::runtime::Builder::new_multi_thread()
        .worker_threads(2)
        .enable_all()
        .thread_name("kvt-fuse-io")
        .build()
        .context("build tokio runtime")?;
    let handle = runtime.handle().clone();

    // Build the FUSE filesystem
    let workdir = dirs_or_default()
        .unwrap_or_else(|| PathBuf::from("/tmp"))
        .join("fuse-work");
    let workdir_clone = workdir.clone();
    std::fs::create_dir_all(&workdir).context("create workdir")?;

    let fs = KvtFs::new(handle, args.api_url, args.api_key, workdir);

    // Mount options
    let mut options: Vec<fuser::MountOption> = vec![
        fuser::MountOption::FSName("kvt-fuse".into()),
        fuser::MountOption::AutoUnmount,
        fuser::MountOption::DefaultPermissions,
        fuser::MountOption::NoExec,
        fuser::MountOption::NoDev,
    ];
    if args.allow_other {
        options.push(fuser::MountOption::AllowOther);
    }

    let session = fuser::spawn_mount2(fs, &args.mountpoint, &options)
        .with_context(|| format!("mount at {}", args.mountpoint.display()))?;

    tracing::info!("kvt-fuse mounted at {}", args.mountpoint.display());

    // Handle SIGTERM / SIGINT
    {
        let handle = runtime.handle().clone();
        handle.spawn(async {
            use tokio::signal;
            let mut sigterm = signal::unix::signal(signal::unix::SignalKind::terminate())
                .expect("register SIGTERM handler");
            let mut sigint = signal::unix::signal(signal::unix::SignalKind::interrupt())
                .expect("register SIGINT handler");

            tokio::select! {
                _ = sigterm.recv() => {}
                _ = sigint.recv() => {}
            }
            SHUTDOWN.store(true, Ordering::SeqCst);
            tracing::info!("shutdown signal received");
        });
    }

    // Wait for shutdown
    while !SHUTDOWN.load(Ordering::SeqCst) {
        std::thread::sleep(std::time::Duration::from_millis(200));
    }

    tracing::info!("unmounting");
    drop(session);
    // Clean up workdir
    std::fs::remove_dir_all(&workdir_clone).ok();
    tracing::info!("kvt-fuse stopped");
    Ok(())
}

fn dirs_or_default() -> Option<PathBuf> {
    if let Ok(home) = std::env::var("KVT_HOME") {
        return Some(PathBuf::from(home));
    }
    if let Ok(home) = std::env::var("HOME") {
        return Some(PathBuf::from(home).join(".kvt"));
    }
    None
}