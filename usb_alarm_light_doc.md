# USB 七色报警灯 控制脚本

## 文件

- 脚本：`usb_alarm_light.py`
- 依赖：`pyserial`（`pip install pyserial`）
- 协议：Modbus RTU / 4800-8-N-1 / 默认站号 01

## 命令速查

```
# 灯光常亮
python usb_alarm_light.py --port COM44 red on
python usb_alarm_light.py --port COM44 yellow on
python usb_alarm_light.py --port COM44 green on
python usb_alarm_light.py --port COM44 white on
python usb_alarm_light.py --port COM44 cyan on
python usb_alarm_light.py --port COM44 purple on
python usb_alarm_light.py --port COM44 blue on

# 闪烁
python usb_alarm_light.py --port COM44 red blink_slow
python usb_alarm_light.py --port COM44 red blink_fast

# 蜂鸣器
python usb_alarm_light.py --port COM44 buzzer on
python usb_alarm_light.py --port COM44 buzzer off

# 蜂鸣+亮灯 N 毫秒（默认 400ms）
python usb_alarm_light.py --port COM44 beep
python usb_alarm_light.py --port COM44 beep red -t 500
python usb_alarm_light.py --port COM44 beep blue -t 200

# 彩虹循环
python usb_alarm_light.py --port COM44 rainbow

# 关闭
python usb_alarm_light.py --port COM44 off

# 调试（显示原始 Modbus 帧）
python usb_alarm_light.py --port COM44 --debug red on
```

## API（Python 调用）

```python
from usb_alarm_light import AlarmLight, Color, FlashMode

light = AlarmLight(port="COM44")  # port=None 自动探测

# 亮灯
light.red("on")          # 红灯常亮
light.green("blink_slow")  # 绿灯慢闪
light.blue("blink_fast")   # 蓝灯快闪
light.set_color("yellow", "on")

# 蜂鸣
light.buzzer(True)   # 开
light.buzzer(False)  # 关

# 蜂鸣+亮灯 N 毫秒
light.beep("red", ms=400)
light.beep("blue", ms=800)

# 关闭
light.off()

# PWM RGB（0-1999）
light.pwm(red=1999, green=0, blue=0)  # 纯红
light.pwm(1999, 1000, 0)             # 橙色 R=100% G=50%
light.pwm(0, 0, 1999, flash_interval_100ms=5, buzzer=True)  # 蓝色闪烁+蜂鸣

# 彩虹
light.rainbow(duration=2.0)

light.close()
```

## 协议细节

| 项目 | 值 |
|------|-----|
| 物理层 | USB 串口 (CH340 等) |
| 波特率 | 4800（默认） |
| 数据位 | 8 |
| 校验 | 无 |
| 停止位 | 1 |
| 协议 | Modbus RTU |
| 站号 | 0x01（默认） |
| 功能码 | 06（写单寄存器）、10（写多寄存器） |

### 直控模式寄存器（FC06, 寄存器 0x00C2）

| 颜色 | 常亮 | 慢闪 | 快闪 |
|------|------|------|------|
| 红 | 0x11 | 0x21 | 0x31 |
| 黄 | 0x12 | 0x22 | 0x32 |
| 绿 | 0x13 | 0x23 | 0x33 |
| 红+喇叭 | 0x14 | 0x24 | 0x34 |
| 白 | 0x15 | 0x25 | 0x35 |
| 青 | 0x16 | 0x26 | 0x36 |
| 紫 | 0x17 | 0x27 | 0x37 |
| 蓝 | 0x18 | 0x28 | 0x38 |

| 功能 | 值 |
|------|-----|
| 喇叭开 | 0x40 |
| 喇叭关 | 0x41 |
| 全关 | 0x60 |

帧格式：`[站号] 06 00 C2 [值H] [值L] [CRCL] [CRCH]`

例：红灯常亮 → `01 06 00 C2 00 11 XX XX`

### PWM 模式（FC10, 起始地址 0x0002, 7 个寄存器）

| 地址 | 功能 |
|------|------|
| 0x02 | 蓝 PWM (0-1999) |
| 0x03 | 绿 PWM (0-1999) |
| 0x04 | 灯开关 (0=关 1=开) |
| 0x05 | 闪烁间隔 (100ms 单位, 0=常亮) |
| 0x06 | 喇叭 (0=关 1=开) |
| 0x07 | 保留 (填 0) |
| 0x08 | 红 PWM (0-1999) |

帧格式：`[站号] 10 00 02 00 07 0E [14字节数据] [CRC]`

## 修改记录

| 日期 | 改动 |
|------|------|
| 2026-06-25 | 初始版本（基于 PDF 说明书） |
| 2026-06-25 | 兼容 Python 2：去掉类型注解、enum、@property、from __future__ import print_function |
| 2026-06-25 | 新增 `--debug` 显示原始帧、`--station` 指定站号 |
| 2026-06-25 | 新增 `beep` 命令（ms 单位，默认 400ms） |
| 2026-06-25 | 性能优化：timeout 1s→0.1s，帧间延迟 80ms→15ms |
| 2026-06-25 | 默认站号 0xFF→0x01 |
| 2026-06-25 | 修复 `rainbow` 崩溃（Color.values() Python 2 兼容） |
