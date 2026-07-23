# PowerWing — 电源供应器 & USB Hub 控制器

跨平台电源供应器与 USB Hub 控制器，支持 Web UI 和 CLI 双模式操作。

---

## 功能特性

### 电源控制 (SPM3051 / PD Pocket)
- 设置输出电压 / 电流
- 开关输出
- OVP / OCP 保护限值设置
- 实时测量（电压、电流、功率、温度）
- CV / CC 模式指示

### USB Hub 控制 (USB-Slim)
- 4 端口独立电源开关
- 锁定 / 硬件按键 / 自动保存设置
- 每端口独立循环开关（Cycle Mode）

### Cycle Mode 循环模式
每个端口可独立配置循环开关：

| 功能 | 说明 |
|------|------|
| 开关 | 独立启用/禁用每个端口的循环 |
| ON 时长 | 范围 0~3600 秒，min/max 区间随机 |
| OFF 时长 | 范围 0~3600 秒，min/max 区间随机 |
| 手动干预 | 手动开关端口后，该端口循环自动暂停 |
| 动态显示 | 实时显示 `ON 剩余/总时长` |

---

## 编译 (Windows 11)

### 前置条件

1. **安装 Go**
   ```powershell
   # 下载 https://go.dev/dl/ 并安装到 C:\Go
   # 验证
   go version
   # 输出: go version go1.23.x windows/amd64
   ```

2. **安装 goversioninfo**（用于嵌入图标和版本信息）
   ```powershell
   go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest
   ```

### 编译步骤

```powershell
# 1. 进入项目目录
cd C:\Work\Tools\power_wing

# 2. 用 build.ps1 编译（自动生成资源文件 + 编译）
.\build.ps1

# 或手动分步：
# goversioninfo -o rsrc_windows.syso
# go build -trimpath -ldflags="-s -w" -o power_wing.exe .

# 3. 编译产物
# power_wing.exe  (约 7MB)
```

### 从 WSL 编译（调用 Windows Go）

```bash
cd /mnt/c/Work/Tools/power_wing

# 生成资源文件
powershell.exe -ExecutionPolicy Bypass -Command \
  "C:\Users\Jason\go\bin\goversioninfo.exe -o rsrc_windows.syso"

# 编译
powershell.exe -ExecutionPolicy Bypass -Command \
  "\$env:Path = 'C:\Go\bin;C:\Users\Jason\go\bin;' + \$env:Path; \
   go build -trimpath -ldflags='-s -w' -o power_wing.exe ."
```

---

## 启动

### Windows 本地

```powershell
# 默认端口 8765，自动打开浏览器
power_wing.exe

# 指定端口，不自动打开浏览器
power_wing.exe --port 8765 --no-browser
```

启动后访问 http://127.0.0.1:8765

### 从 WSL 启动（Windows 进程）

```bash
/mnt/c/Work/Tools/power_wing/power_wing.exe --port 8765 --no-browser
```

> 注意：WSL 中启动的 Windows 进程绑定在 `0.0.0.0:8765`，可从局域网访问。
> 建议用 `nohup` 或直接在 Windows 上双击启动，避免被 WSL shell 生命周期影响。

---

## Web UI 使用

浏览器打开 http://[IP]:8765

### PSU 页面
- 实时测量值（V/A/W）+ 动态进度条
- 输出开关
- 电压/电流设置（spinbox + 滑动条）
- OVP/OCP 保护设置
- Cycle Mode 面板

### Hub 页面
- 4 端口独立开关
- 锁定/硬件按键/自动保存
- 每端口 Cycle Mode（可独立折叠/展开）
  - 循环开关
  - ON 时长 min/max（整数秒）
  - OFF 时长 min/max（整数秒）
  - 动态状态显示 `ON 2.3/5.0`
  - 循环开启时输入框禁用灰显，关闭时可编辑

---

## CLI 命令

### 端口控制

```powershell
# 查看可用串口
power_wing.exe --list-ports

# 列出已配置设备
power_wing.exe --list-devices

# 单个命令控制（执行后退出）
power_wing.exe --device COM24 --usb-port 1 --usb-state on
power_wing.exe --device COM24 --usb-port 3 --usb-state off
```

### 电源控制（用于 SPM3051 / PD Pocket）

```bash
power_wing.exe --device psu1 --volt 12.0
power_wing.exe --device psu1 --curr 2.5
power_wing.exe --device psu1 --outp on
power_wing.exe --device psu1 --outp off
power_wing.exe --device psu1 --ovp 15.0
power_wing.exe --device psu1 --ocp 3.0
```

---

## REST API（远程控制）

所有 API 通过 HTTP 访问，端口 8765。

### 目标 IP

| 场景 | IP | 说明 |
|------|----|------|
| Win11 本机 | `127.0.0.1` 或 `localhost` | 直接在 Windows 上访问 |
| WSL 环境 | `10.30.2.247` | Windows 对外 IP，固定不变 |
| WSL 环境（备选） | `172.27.112.1` | WSL 网关 IP，WSL2 重启可能变化 |
| 局域网其他电脑 | `10.30.2.247` | 同一局域网可直接访问 |

> 以下示例以 `WIN_IP` 代替目标 IP，根据场景替换。

### 设备列表

```bash
curl -s http://WIN_IP:8765/api/devices
```

### 端口手动控制

```bash
# 打开 PORT 1
curl -s -X POST http://WIN_IP:8765/api/device/COM24/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"set_usb_port","params":{"port":1,"on":true}}'

# 关闭 PORT 3
curl -s -X POST http://WIN_IP:8765/api/device/COM24/cmd \
  -H "Content-Type: application/json" \
  -d '{"command":"set_usb_port","params":{"port":3,"on":false}}'
```

### 读取循环配置

```bash
# 读取 PORT 1 的循环配置
curl -s "http://WIN_IP:8765/api/device/COM24/cycle?port=1"

# 读取所有端口
for p in 1 2 3 4; do
  echo "=== Port $p ==="
  curl -s "http://WIN_IP:8765/api/device/COM24/cycle?port=$p"
  echo
done
```

### 设置循环配置

```bash
# 开启 PORT 1 循环（ON 2~5秒，OFF 1~3秒）
curl -s -X PUT http://WIN_IP:8765/api/device/COM24/cycle?port=1 \
  -H "Content-Type: application/json" \
  -d '{"enabled":true,"on_min":2,"on_max":5,"off_min":1,"off_max":3}'

# 关闭 PORT 2 循环
curl -s -X PUT http://WIN_IP:8765/api/device/COM24/cycle?port=2 \
  -H "Content-Type: application/json" \
  -d '{"enabled":false,"on_min":1,"on_max":3,"off_min":1,"off_max":3}'
```

### 查看设备实时状态

```bash
# 获取所有设备最新状态（含循环阶段信息）
curl -s http://WIN_IP:8765/api/devices
```

响应中 `cycle_per_port` 字段包含：
```json
"cycle_per_port": [
  {"port":1,"enabled":true,"phase":"on","remain":4.3,"total":5.0},
  {"port":2,"enabled":false,"phase":"","remain":0,"total":0},
  ...
]
```

### JSON 参数说明

| 字段 | 类型 | 范围 | 说明 |
|------|------|------|------|
| `enabled` | bool | true/false | 启用/禁用循环 |
| `on_min` | int | 0~3600 | ON 阶段最小秒数 |
| `on_max` | int | 0~3600 | ON 阶段最大秒数 |
| `off_min` | int | 0~3600 | OFF 阶段最小秒数 |
| `off_max` | int | 0~3600 | OFF 阶段最大秒数 |

约束：`min ≤ max` 自动钳制。

---

## 项目架构

```
power_wing/
├── main.go                   # 入口
├── internal/
│   ├── config/               # 配置文件读写 (~/.config/PowerWing/config.json)
│   ├── device/
│   │   ├── device.go         # Device / PowerSupply / USBHub 接口定义
│   │   ├── spm3051/          # SPM3051 电源驱动（SCPI 协议）
│   │   ├── pdpocket/         # PD Pocket 电源驱动
│   │   └── usbslim/          # USB-Slim Hub 驱动（二进制帧协议）
│   ├── manager/              # 设备生命周期管理 + 循环引擎
│   ├── server/               # HTTP + WebSocket 服务器
│   ├── tray/                 # 系统托盘（Windows）
│   └── voice/                # 语音命令（Windows）
└── web/                      # 前端 SPA（纯 Vanilla JS）
    ├── index.html
    ├── app.js
    └── style.css
```
