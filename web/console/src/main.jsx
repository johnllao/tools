import { Fragment, StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './styles.css'

function App() {
    return (
        <div className="app-layout">
            <aside className="sidebar">
                <nav>
                    <h2 className="sidebar-title">Navigator</h2>
                    <ul className="nav-list">
                        <li className="nav-item active">Overview</li>
                        <li className="nav-item">Logs</li>
                        <li className="nav-item">Files</li>
                        <li className="nav-item">Settings</li>
                    </ul>
                </nav>
            </aside>
            <main className="content">
                <h1>Welcome!</h1>
                <p>Select an option from the navigator to get started.</p>
            </main>
        </div>
    )
}

createRoot(document.getElementById('root')).render(
    <StrictMode>
        <App />
    </StrictMode>,
)
