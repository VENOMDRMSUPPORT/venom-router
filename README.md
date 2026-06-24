# Venom Router Dev Server Manager

This utility provides a CLI script and a styled Windows Forms desktop application to manage the local Vite + React development server.

---

## 🚀 CLI Commands (For Agents & Developers)

The CLI manager is located at `scripts/dev.ps1` inside the `scripts` directory. It is designed to be fully automated and easy for any agent (AI agent, CI pipeline, or terminal script) to query and control.

To run these commands, use **PowerShell** (or bypass policy if running from cmd/bash):

### 1. Check Server Status

Check if the development server is running, starting, or stopped.

```powershell
powershell.exe -ExecutionPolicy Bypass -File .\scripts\dev.ps1 status
```

- **Returns `RUNNING`**: Server is up and listening on port 8081.
- **Returns `STARTING`**: Server process is launching but not yet responsive.
- **Returns `STOPPED`**: Server is down.

### 2. Start the Server

Launch the dev server in a hidden background command window (via `bun dev`).

```powershell
powershell.exe -ExecutionPolicy Bypass -File .\scripts\dev.ps1 start
```

- Will automatically wait and verify the port becomes active before returning `Ready!`.

### 3. Stop the Server

Gracefully kill any running dev server processes and clear the port.

```powershell
powershell.exe -ExecutionPolicy Bypass -File .\scripts\dev.ps1 stop
```

### 4. Restart the Server

Stop the server, release the port, and boot up a clean new dev process.

```powershell
powershell.exe -ExecutionPolicy Bypass -File .\scripts\dev.ps1 restart
```

### 5. Check Live logs

View the last 40 lines of standard output and the last 10 lines of standard error logs.

```powershell
powershell.exe -ExecutionPolicy Bypass -File .\scripts\dev.ps1 logs
```

---

## 🖥️ Desktop GUI Manager

A beautiful, custom dark-themed GUI manager is available to monitor and manage the dev server interactively:

- **Launch GUI**: Run `Dev Server.vbs` in the root of the project, or execute `powershell.exe -STA -WindowStyle Hidden -ExecutionPolicy Bypass -File .\scripts\dev-gui.ps1`.
- **Features**:
  - Live state updates (every 500ms).
  - Custom pill scrollbar with mouse drag and track-click support.
  - Padded auto-scrolling terminal output box.
  - Interactive rounded control buttons with hover transitions.
  - Uses the custom project logo as header logo and window icon.
