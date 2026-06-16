/* eslint-disable react-refresh/only-export-components */

import { StrictMode, useState, useEffect, useRef, useCallback } from 'react'
import { createRoot } from 'react-dom/client'
import './styles.css'

// Maximum number of log lines to keep in memory before trimming.
const MAX_LOG_LINES = 10000

function App() {
    // ── State ─────────────────────────────────────────────────────────
    const [logs, setLogs] = useState([])
    const [status, setStatus] = useState('connecting') // connecting | connected | disconnected
    const [paused, setPaused] = useState(false)
    const [logPath, setLogPath] = useState('Connecting...')
    const [clientCount, setClientCount] = useState(0)

    const logRef = useRef(null) // scrollable log container
    const esRef = useRef(null) // EventSource instance
    const pausedRef = useRef(false) // mutable copy of paused for scroll effect

    // Keep the ref in sync so the scroll effect always reads the latest
    // paused value without needing to re-run when paused changes.
    useEffect(() => {
        pausedRef.current = paused
    }, [paused])

    // ── SSE connection ────────────────────────────────────────────────
    useEffect(() => {
        function connect() {
            // Close any previous connection before opening a new one.
            if (esRef.current) {
                esRef.current.close()
            }
            setStatus('connecting')

            const es = new EventSource('/events')
            esRef.current = es

            // Fires when the TCP connection is established.
            es.onopen = () => {
                setStatus('connected')
            }

            // The server sends a named "connected" event after the history
            // batch with metadata (logPath, clientCount).
            es.addEventListener('connected', (e) => {
                try {
                    const data = JSON.parse(e.data)
                    if (data.logPath) {
                        setLogPath(data.logPath)
                    }
                    if (data.clientCount !== undefined) {
                        setClientCount(data.clientCount)
                    }
                } catch {
                    // Ignore malformed JSON — the metadata is non-critical.
                }
                setStatus('connected')
            })

            // Default handler for unnamed "data:" events — each message is
            // one log line.
            es.onmessage = (e) => {
                const line = e.data
                setLogs((prev) => {
                    const next = [...prev, line]
                    // Cap memory usage by trimming when we exceed the limit.
                    if (next.length > MAX_LOG_LINES) {
                        return next.slice(-Math.floor(MAX_LOG_LINES / 2))
                    }
                    return next
                })
            }

            // The browser's EventSource auto-reconnects on error. We just
            // update the status so the UI reflects the current state.
            es.onerror = () => {
                setStatus('disconnected')
                // Do NOT close the EventSource — let the browser retry.
            }
        }

        connect()

        // Clean up on unmount.
        return () => {
            if (esRef.current) {
                esRef.current.close()
            }
        }
    }, [])

    // ── Auto-scroll ───────────────────────────────────────────────────
    // Scroll to the bottom on every render when new logs arrive, unless
    // the user has paused scrolling. We read pausedRef.current so this
    // effect does not need paused in its dependency array.
    useEffect(() => {
        if (!pausedRef.current && logRef.current) {
            logRef.current.scrollTop = logRef.current.scrollHeight
        }
    })

    // ── Handlers ──────────────────────────────────────────────────────
    const handleClear = useCallback(() => {
        setLogs([])
    }, [])

    const handleTogglePause = useCallback(() => {
        setPaused((prev) => !prev)
    }, [])

    // ── Status display ────────────────────────────────────────────────
    const statusLabel = {
        connected: 'Connected',
        connecting: 'Connecting...',
        disconnected: 'Disconnected',
    }

    // ── Render ────────────────────────────────────────────────────────
    return (
        <div className="app">
            <header className="header">
                <div className="header-left">
                    <span className="header-title">&gt; tail</span>
                    <span className="header-path" title={logPath}>
                        {logPath}
                    </span>
                </div>
                <div className="header-right">
                    <span className="status-indicator">
                        <span className={`status-dot status-${status}`} />
                        <span className="status-label">
                            {statusLabel[status] || status}
                        </span>
                        {clientCount > 1 && (
                            <span className="status-clients">
                                ({clientCount} connected)
                            </span>
                        )}
                    </span>
                    <button
                        className="btn"
                        onClick={handleTogglePause}
                        type="button"
                    >
                        {paused ? '▶ Resume' : '⏸ Pause'}
                    </button>
                    <button
                        className="btn btn-clear"
                        onClick={handleClear}
                        type="button"
                    >
                        &#x2715; Clear
                    </button>
                </div>
            </header>

            <main className="log-area" ref={logRef}>
                {logs.length === 0 && (
                    <div className="log-empty">
                        {status === 'connected'
                            ? 'Waiting for log entries...'
                            : 'Connecting to server...'}
                    </div>
                )}
                {logs.map((line, i) => (
                    <div key={i} className="log-line">
                        {line}
                    </div>
                ))}
            </main>
        </div>
    )
}

createRoot(document.getElementById('root')).render(
    <StrictMode>
        <App />
    </StrictMode>,
)
