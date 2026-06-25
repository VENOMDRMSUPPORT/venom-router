# Venom Dev Launcher

This utility provides CLI scripts and a styled Windows Forms desktop application to manage:

- **Venom Router** — local Vite + React dev server on port `8081`
- **Codex Proxy** — `api2codex` bridge for Codex Desktop + OpenCode Zen on port `8000`

---

## 🚀 CLI Commands (For Agents & Developers)

### Venom Router — `scripts/dev.ps1`

The CLI manager is located at `scripts/dev.ps1`. It is designed to be fully automated and easy for any agent (AI agent, CI pipeline, or terminal script) to query and control.

To run these commands, use **PowerShell** (or bypass policy if running from cmd/bash):

#### 1. Check Server Status

Check if the development server is running, starting, or stopped.

```powershell
powershell.exe -ExecutionPolicy Bypass -File .\scripts\dev.ps1 status
```

- **Returns `RUNNING`**: Server is up and listening on port 8081.
- **Returns `STARTING`**: Server process is launching but not yet responsive.
- **Returns `STOPPED`**: Server is down.

#### 2. Start the Server

Launch the dev server in a hidden background command window (via `bun dev`).

```powershell
powershell.exe -ExecutionPolicy Bypass -File .\scripts\dev.ps1 start
```

- Will automatically wait and verify the port becomes active before returning `Ready!`.

#### 3. Stop the Server

Gracefully kill any running dev server processes and clear the port.

```powershell
powershell.exe -ExecutionPolicy Bypass -File .\scripts\dev.ps1 stop
```

#### 4. Restart the Server

Stop the server, release the port, and boot up a clean new dev process.

```powershell
powershell.exe -ExecutionPolicy Bypass -File .\scripts\dev.ps1 restart
```

#### 5. Check Live logs

View the last 40 lines of standard output and the last 10 lines of standard error logs.

```powershell
powershell.exe -ExecutionPolicy Bypass -File .\scripts\dev.ps1 logs
```

### Codex Proxy — `scripts/codex.ps1`

Manages the local `api2codex` proxy used by Codex Desktop with OpenCode Zen (`%USERPROFILE%\.codex\api2codex`).

```powershell
powershell.exe -ExecutionPolicy Bypass -File .\scripts\codex.ps1 status
powershell.exe -ExecutionPolicy Bypass -File .\scripts\codex.ps1 start
powershell.exe -ExecutionPolicy Bypass -File .\scripts\codex.ps1 stop
powershell.exe -ExecutionPolicy Bypass -File .\scripts\codex.ps1 restart
powershell.exe -ExecutionPolicy Bypass -File .\scripts\codex.ps1 logs
```

- **Health endpoint**: `http://127.0.0.1:8000/health` (returns `{"status":"ok"}` when ready)
- **Logs**: `.dev\codex.out` and `.dev\codex.err`

---

## 🖥️ Desktop GUI Manager

A beautiful, custom dark-themed GUI manager is available to monitor and manage both services interactively:

- **Launch GUI**: Run `Dev Server.vbs` in the root of the project, or execute `powershell.exe -STA -WindowStyle Hidden -ExecutionPolicy Bypass -File .\scripts\dev-gui.ps1`.
- **Tabs**:
  - **VENOM ROUTER** — manage port `8081` via `dev.ps1`
  - **CODEX** — manage port `8000` via `codex.ps1`
- **Start All / Stop All** — boot or shut down both services with one click (useful after a reboot).
- **Features**:
  - Live state updates (every 500ms).
  - Custom pill scrollbar with mouse drag and track-click support.
  - Padded auto-scrolling terminal output box per tab.
  - Interactive rounded control buttons with hover transitions.
  - Uses the custom project logo as header logo and window icon.

### After a reboot

1. Double-click `Dev Server.vbs`
2. Click **Start All**
3. Open Codex Desktop manually when you need it (the launcher does not auto-open Codex)
4. Verify Codex proxy: `http://127.0.0.1:8000/health`
5. Verify Venom Router: `http://localhost:8081`
