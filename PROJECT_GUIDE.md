# Quick Start Guide - PipelineGen

Welcome to **PipelineGen**, a powerful Go-based backend platform for media processing automation.

## 🛠 System Requirements

Before starting, make sure you have installed:
- **Go**: Version 1.25 or higher (recommended 1.25.9+)
- **Python**: Version 3.10+ (for embedding and indexing scripts)
- **yt-dlp**: Installed and available in your system PATH
- **FFmpeg**: For audio/video cutting and encoding

## 🚀 Installation and Startup

1. **Configure the environment**:
   Copy the configuration file and fill in your keys and credentials (e.g., Google Drive API):
   ```bash
   cp config.example.yaml config.yaml
   ```

2. **Build the server**:
   ```bash
   go build -o pipelinegen ./cmd/server/
   ```

3. **Start PipelineGen**:
   Run the backend in full mode (HTTP Server + Async Workers):
   ```bash
   ./pipelinegen --mode all
   ```

## 📂 Database Structure

PipelineGen uses centralized SQLite in WAL mode with two databases:
- `data/velox/velox.db.sqlite`: Main database for Scripts, Jobs and Asset Index.
- `data/media/media.db.sqlite`: Unified media database (YouTube, Artlist, Stock, Images, Voiceovers).
