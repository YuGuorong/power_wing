#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""USB 7-Color Alarm Light Controller (Python 2/3 compatible)."""
from __future__ import print_function

import argparse
import struct
import sys
import time

# ── Enum fallback (Python 2 compatible) ─────────────────────────────────

class Color(object):
    RED = "red"
    YELLOW = "yellow"
    GREEN = "green"
    WHITE = "white"
    CYAN = "cyan"
    PURPLE = "purple"
    BLUE = "blue"

    _ALL = ["red", "yellow", "green", "white", "cyan", "purple", "blue"]

    @staticmethod
    def values():
        return Color._ALL

    @staticmethod
    def from_str(s):
        if s in Color._ALL:
            return s
        raise ValueError("Unknown color: %s" % s)


class FlashMode(object):
    STEADY = "on"
    SLOW = "blink_slow"
    FAST = "blink_fast"

    _ALL = ["on", "blink_slow", "blink_fast"]

    @staticmethod
    def values():
        return FlashMode._ALL

    @staticmethod
    def from_str(s):
        if s in FlashMode._ALL:
            return s
        raise ValueError("Unknown flash mode: %s" % s)


# ── Modbus CRC16 ────────────────────────────────────────────────────────

_MODBUS_CRC_TABLE = None


def _build_crc_table():
    global _MODBUS_CRC_TABLE
    table = []
    for i in range(256):
        crc = i
        for _ in range(8):
            if crc & 1:
                crc = (crc >> 1) ^ 0xA001
            else:
                crc >>= 1
        table.append(crc)
    _MODBUS_CRC_TABLE = table


def modbus_crc16(data):
    global _MODBUS_CRC_TABLE
    if _MODBUS_CRC_TABLE is None:
        _build_crc_table()
    crc = 0xFFFF
    for byte in data:
        if isinstance(byte, str):
            byte = ord(byte)
        idx = (crc ^ byte) & 0xFF
        crc = (_MODBUS_CRC_TABLE[idx] ^ (crc >> 8)) & 0xFFFF
    return crc


def modbus_crc16_bytes(data):
    crc = modbus_crc16(data)
    return struct.pack('<H', crc)


# ── Command definitions ──────────────────────────────────────────────────

_DIRECT_CMDS = {
    Color.RED:    (0x11, 0x21, 0x31),
    Color.YELLOW: (0x12, 0x22, 0x32),
    Color.GREEN:  (0x13, 0x23, 0x33),
    Color.WHITE:  (0x15, 0x25, 0x35),
    Color.CYAN:   (0x16, 0x26, 0x36),
    Color.PURPLE: (0x17, 0x27, 0x37),
    Color.BLUE:   (0x18, 0x28, 0x38),
}


def _direct_value(color, mode):
    modes = _DIRECT_CMDS[color]
    if mode == FlashMode.STEADY:
        return modes[0]
    elif mode == FlashMode.SLOW:
        return modes[1]
    else:
        return modes[2]


DIRECT_BUZZER_ON   = 0x40
DIRECT_BUZZER_OFF  = 0x41
DIRECT_ALL_OFF     = 0x60

DIRECT_STATION = 0x01
PWM_STATION    = 0x01


# ── Modbus frame builders ────────────────────────────────────────────────

def build_fc06(station, register_addr, value):
    frame = struct.pack('>BBHH', station, 0x06, register_addr, value)
    return frame + modbus_crc16_bytes(frame)


def build_fc10(station, start_addr, values):
    num_regs = len(values)
    byte_count = num_regs * 2
    header = struct.pack('>BBHHB', station, 0x10, start_addr, num_regs, byte_count)
    body = b''.join(struct.pack('>H', v) for v in values)
    frame = header + body
    return frame + modbus_crc16_bytes(frame)


# ── Serial port helpers ──────────────────────────────────────────────────

def _find_serial_port(port):
    if port:
        return port
    import glob
    candidates = glob.glob('/dev/ttyUSB*') + glob.glob('/dev/ttyACM*')
    if not candidates:
        candidates.extend(glob.glob('/dev/ttyS*'))
    if not candidates:
        raise RuntimeError(
            "No serial port found. Specify with --port (e.g. COM44)."
        )
    return candidates[0]


# ── AlarmLight controller ────────────────────────────────────────────────

class AlarmLight(object):

    def __init__(self, port=None, baudrate=4800, timeout=0.1, debug=False):
        import serial
        self._port_path = _find_serial_port(port)
        self._debug = debug
        self._ser = serial.Serial(
            port=self._port_path,
            baudrate=baudrate,
            bytesize=serial.EIGHTBITS,
            parity=serial.PARITY_NONE,
            stopbits=serial.STOPBITS_ONE,
            timeout=timeout,
        )

    def get_port(self):
        return self._port_path

    def close(self):
        if self._ser and self._ser.is_open:
            self._ser.close()
    def _send_raw(self, frame, debug=False):
        """Send a raw Modbus frame and return the response."""
        # Modbus RTU: 3.5 char silent interval ~7ms at 4800 baud
        time.sleep(0.008)
        self._ser.reset_input_buffer()
        self._ser.write(frame)
        self._ser.flush()
        if debug:
            print("TX (%d bytes): %s" % (len(frame), ' '.join('%02X' % b for b in bytearray(frame))))
        time.sleep(0.015)
        resp = self._ser.read(32)
        if debug:
            if resp:
                print("RX (%d bytes): %s" % (len(resp), ' '.join('%02X' % b for b in bytearray(resp))))
            else:
                print("RX: (no response)")
        return resp

    # ── Direct control (station 0xFF, FC06) ───────────────────────────

    def set_color(self, color, mode=FlashMode.STEADY, station=None):
        if station is None:
            station = DIRECT_STATION
        if isinstance(color, str):
            color = Color.from_str(color)
        if isinstance(mode, str):
            mode = FlashMode.from_str(mode)
        val = _direct_value(color, mode)
        frame = build_fc06(station, 0x00C2, val)
        if self._debug:
            print("DEBUG station=0x%02X color=%s mode=%s val=0x%04X" %
                  (station, color, mode, val))
        return self._send_raw(frame, debug=self._debug)

    def red(self, mode="on"):
        return self.set_color(Color.RED, FlashMode.from_str(mode))

    def yellow(self, mode="on"):
        return self.set_color(Color.YELLOW, FlashMode.from_str(mode))

    def green(self, mode="on"):
        return self.set_color(Color.GREEN, FlashMode.from_str(mode))

    def white(self, mode="on"):
        return self.set_color(Color.WHITE, FlashMode.from_str(mode))

    def cyan(self, mode="on"):
        return self.set_color(Color.CYAN, FlashMode.from_str(mode))

    def purple(self, mode="on"):
        return self.set_color(Color.PURPLE, FlashMode.from_str(mode))

    def blue(self, mode="on"):
        return self.set_color(Color.BLUE, FlashMode.from_str(mode))

    def buzzer(self, on=True, station=None):
        if station is None:
            station = DIRECT_STATION
        val = DIRECT_BUZZER_ON if on else DIRECT_BUZZER_OFF
        frame = build_fc06(station, 0x00C2, val)
        return self._send_raw(frame, debug=self._debug)

    def off(self, station=None):
        if station is None:
            station = DIRECT_STATION
        frame = build_fc06(station, 0x00C2, DIRECT_ALL_OFF)
        return self._send_raw(frame, debug=self._debug)

    # ── Beep (alarm on for N seconds) ─────────────────────────────────

    def beep(self, color=Color.RED, ms=400, station=None):
        """Alarm on for N milliseconds, then off.

        color: light color (default: red)
        ms: duration in milliseconds (default: 400)
        """
        if station is None:
            station = DIRECT_STATION
        if isinstance(color, str):
            color = Color.from_str(color)
        val = _direct_value(color, FlashMode.STEADY)
        frame_on = build_fc06(station, 0x00C2, val)
        self._send_raw(frame_on, debug=self._debug)
        frame_buzz = build_fc06(station, 0x00C2, DIRECT_BUZZER_ON)
        self._send_raw(frame_buzz, debug=self._debug)
        if self._debug:
            print("Beep: %s for %dms" % (color, ms))
        time.sleep(ms / 1000.0)
        frame_off = build_fc06(station, 0x00C2, DIRECT_ALL_OFF)
        return self._send_raw(frame_off, debug=self._debug)

    # ── PWM control (station 0x01) ────────────────────────────────────

    def pwm(self, red, green, blue, flash_interval_100ms=0, buzzer=False):
        r = max(0, min(1999, int(red)))
        g = max(0, min(1999, int(green)))
        b = max(0, min(1999, int(blue)))
        flash = max(0, min(100, int(flash_interval_100ms)))
        values = [b, g, 1, flash, 1 if buzzer else 0, 0, r]
        frame = build_fc10(PWM_STATION, 0x0002, values)
        return self._send_raw(frame)

    def pwm_off(self):
        frame = build_fc06(PWM_STATION, 0x0004, 0x0000)
        return self._send_raw(frame)

    # ── Rainbow demo ─────────────────────────────────────────────────

    def rainbow(self, duration=2.0, pause=0.3, station=None):
        if station is None:
            station = DIRECT_STATION
        colors = Color.values()
        for color in colors:
            sys.stdout.write("  " + color)
            sys.stdout.flush()
            self.set_color(color, FlashMode.STEADY, station=station)
            time.sleep(duration / len(colors))
        sys.stdout.write("\n")
        sys.stdout.flush()
        time.sleep(pause)
        self.off(station=station)


# ── CLI ──────────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(
        description="USB 7-Color Alarm Light Controller (Modbus RTU)",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""Examples:
  %(prog)s red on              Red light, steady on
  %(prog)s green blink_slow    Green slow blink
  %(prog)s blue blink_fast     Blue fast blink
  %(prog)s buzzer on           Turn buzzer on
  %(prog)s off                 Turn everything off
  %(prog)s beep red            Red alarm 400ms (default)
  %(prog)s beep blue -t 800    Blue alarm 800ms
  %(prog)s pwm 1999 1000 0     PWM: R=100%% G=50%% B=0
  %(prog)s rainbow             Cycle all 7 colors
  %(prog)s --port COM44 red on   Specify port
""",
    )
    parser.add_argument('--port', '-p', help='Serial port')
    parser.add_argument('--baudrate', '-b', type=int, default=4800,
                        help='Baud rate (default: 4800)')
    parser.add_argument('--station', '-s', type=lambda x: int(x, 0), default=None,
                        help='Station ID (default: 0xFF for direct, 0x01 for PWM). '
                             'Use 0x prefix for hex, e.g. 0xFF')
    parser.add_argument('--debug', '-d', action='store_true',
                        help='Show raw Modbus frames')

    sub = parser.add_subparsers(dest='command', help='Command')

    for c in Color.values():
        p = sub.add_parser(c, help='%s light' % c)
        p.add_argument('mode', nargs='?', default='on',
                       choices=['on', 'blink_slow', 'blink_fast'])

    p = sub.add_parser('buzzer', help='Control buzzer')
    p.add_argument('state', choices=['on', 'off'])

    sub.add_parser('off', help='Turn off light and buzzer')

    p = sub.add_parser('pwm', help='PWM mode (RGB control)')
    p.add_argument('red', type=int, help='Red PWM (0-1999)')
    p.add_argument('green', type=int, help='Green PWM (0-1999)')
    p.add_argument('blue', type=int, help='Blue PWM (0-1999)')
    p.add_argument('--flash', type=int, default=0)
    p.add_argument('--buzzer', action='store_true')

    p = sub.add_parser('rainbow', help='Cycle all 7 colors')
    p.add_argument('--duration', type=float, default=2.0)
    p.add_argument('--pause', type=float, default=0.3)

    p = sub.add_parser('beep', help='Alarm on for N ms (default 400ms)')
    p.add_argument('color', nargs='?', default='red',
                   choices=['red', 'yellow', 'green', 'white', 'cyan', 'purple', 'blue'],
                   help='Light color (default: red)')
    p.add_argument('--time', '-t', type=int, default=400,
                   help='Duration in milliseconds (default: 400)')

    args = parser.parse_args()

    if not args.command:
        parser.print_help()
        sys.exit(1)

    try:
        light = AlarmLight(port=args.port, baudrate=args.baudrate, debug=args.debug)
    except Exception as e:
        sys.stderr.write("Error opening serial port: %s\n" % e)
        sys.exit(1)

    try:
        station = args.station
        print("Connected: %s @ %d baud" % (light.get_port(), args.baudrate))

        if args.command in Color.values():
            # Try default station, then fall back to 0x01 if no response
            if station is None:
                station = DIRECT_STATION
            resp = light.set_color(args.command, args.mode, station=station)
            print("-> %s %s (station=0x%02X)" % (args.command, args.mode, station))
            if args.debug and not resp and station == DIRECT_STATION:
                print("No response from 0xFF, trying station 0x01...")
                resp = light.set_color(args.command, args.mode, station=0x01)
                print("-> retry station=0x01")

        elif args.command == 'buzzer':
            if station is None:
                station = DIRECT_STATION
            light.buzzer(args.state == 'on', station=station)
            print("-> buzzer %s (station=0x%02X)" % (args.state, station))

        elif args.command == 'off':
            if station is None:
                station = DIRECT_STATION
            resp = light.off(station=station)
            print("-> all off (station=0x%02X)" % station)
            if args.debug and not resp and station == DIRECT_STATION:
                print("No response from 0xFF, trying station 0x01...")
                light.off(station=0x01)
                print("-> retry station=0x01")

        elif args.command == 'pwm':
            if station is None:
                station = PWM_STATION
            light.pwm(args.red, args.green, args.blue, args.flash, args.buzzer)
            print("-> PWM R=%d G=%d B=%d flash=%d buzz=%s (station=0x%02X)" %
                  (args.red, args.green, args.blue, args.flash, args.buzzer, station))

        elif args.command == 'rainbow':
            if station is None:
                station = DIRECT_STATION
            print("Rainbow: (station=0x%02X)" % station)
            light.rainbow(args.duration, args.pause, station=station)
            print("-> done")

        elif args.command == 'beep':
            if station is None:
                station = DIRECT_STATION
            print("Beep: %s for %dms (station=0x%02X)" %
                  (args.color, args.time, station))
            light.beep(color=args.color, ms=args.time, station=station)
            print("-> done")

    finally:
        light.close()


if __name__ == '__main__':
    main()
