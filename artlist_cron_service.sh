#!/bin/bash
# Artlist Cron Manager - Service Management Script
# Usage: ./artlist_cron_service.sh [start|stop|status|restart|logs]

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CRON_SCRIPT="$SCRIPT_DIR/scripts/artlist_cron_manager.py"
PID_FILE="$SCRIPT_DIR/data/artlist_cron.pid"
LOG_FILE="/tmp/artlist_cron.log"

start() {
    if [ -f "$PID_FILE" ]; then
        PID=$(cat "$PID_FILE")
        if ps -p $PID > /dev/null 2>&1; then
            echo "⚠️  Cron manager is already running (PID: $PID)"
            return 1
        else
            echo "🗑️  Removing stale PID file"
            rm -f "$PID_FILE"
        fi
    fi

    echo "🚀 Starting Artlist Cron Manager..."
    cd "$SCRIPT_DIR"
    nohup python3 "$CRON_SCRIPT" start > "$LOG_FILE" 2>&1 &
    PID=$!
    echo $PID > "$PID_FILE"
    
    sleep 2
    if ps -p $PID > /dev/null 2>&1; then
        echo "✅ Cron manager started (PID: $PID)"
        echo "📄 Log file: $LOG_FILE"
    else
        echo "❌ Failed to start cron manager"
        echo "📄 Check log: $LOG_FILE"
        return 1
    fi
}

stop() {
    if [ ! -f "$PID_FILE" ]; then
        echo "⚠️  Cron manager is not running"
        return 1
    fi

    PID=$(cat "$PID_FILE")
    echo "🛑 Stopping Artlist Cron Manager (PID: $PID)..."
    
    if ps -p $PID > /dev/null 2>&1; then
        kill $PID
        sleep 2
        
        if ps -p $PID > /dev/null 2>&1; then
            echo "⚠️  Process didn't stop, force killing..."
            kill -9 $PID
        fi
        
        echo "✅ Cron manager stopped"
    else
        echo "⚠️  Process not found"
    fi
    
    rm -f "$PID_FILE"
}

status() {
    if [ -f "$PID_FILE" ]; then
        PID=$(cat "$PID_FILE")
        if ps -p $PID > /dev/null 2>&1; then
            echo "✅ Cron manager is running (PID: $PID)"
            echo ""
            ps -p $PID -o pid,etime,cmd
        else
            echo "❌ PID file exists ($PID) but process is not running"
            echo "🗑️  Removing stale PID file"
            rm -f "$PID_FILE"
        fi
    else
        echo "❌ Cron manager is not running"
    fi
    
    echo ""
    echo "📄 Recent runs:"
    python3 "$CRON_SCRIPT" status 2>&1 | grep -A 20 "RECENT RUNS"
}

logs() {
    if [ -f "$LOG_FILE" ]; then
        echo "📄 Log file: $LOG_FILE"
        echo "Press Ctrl+C to stop watching"
        echo ""
        tail -f "$LOG_FILE"
    else
        echo "⚠️  No log file found: $LOG_FILE"
    fi
}

restart() {
    stop
    sleep 2
    start
}

case "$1" in
    start)
        start
        ;;
    stop)
        stop
        ;;
    status)
        status
        ;;
    logs)
        logs
        ;;
    restart)
        restart
        ;;
    *)
        echo "Usage: $0 {start|stop|status|logs|restart}"
        echo ""
        echo "Commands:"
        echo "  start    - Start the cron manager"
        echo "  stop     - Stop the cron manager"
        echo "  status   - Show current status"
        echo "  logs     - Watch log file"
        echo "  restart  - Restart the service"
        exit 1
        ;;
esac
