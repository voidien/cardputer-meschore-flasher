# Decode a run.mjs recording (.jsonl or .jsonl.gz) into a readable host->device command sequence.
# Copied from the meshflash research prototype (proto-esp32/decode.py); added: sleeps, gzip input.
import gzip
import json, struct, sys

OPS = {0x02: 'FLASH_BEGIN', 0x03: 'FLASH_DATA', 0x04: 'FLASH_END', 0x05: 'MEM_BEGIN', 0x06: 'MEM_END',
       0x07: 'MEM_DATA', 0x08: 'SYNC', 0x09: 'WRITE_REG', 0x0a: 'READ_REG', 0x0b: 'SPI_SET_PARAMS',
       0x0d: 'SPI_ATTACH', 0x0f: 'CHANGE_BAUDRATE', 0x10: 'FLASH_DEFL_BEGIN', 0x11: 'FLASH_DEFL_DATA',
       0x12: 'FLASH_DEFL_END', 0x13: 'SPI_FLASH_MD5', 0x14: 'GET_SECURITY_INFO', 0xd0: 'ERASE_FLASH'}


def unslip(b):
    out, esc = bytearray(), False
    assert b[0] == 0xc0 and b[-1] == 0xc0
    for x in b[1:-1]:
        if esc:
            out.append(0xc0 if x == 0xdc else 0xdb); esc = False
        elif x == 0xdb:
            esc = True
        else:
            out.append(x)
    return bytes(out)


def main(path, full=False):
    opener = gzip.open if path.endswith('.gz') else open
    rows = [json.loads(l) for l in opener(path, 'rt') if l.strip()]
    for r in rows:
        ev = r.get('ev')
        t = r.get('t', '')
        if ev == 'signals':
            s = ' '.join(f'{k}={int(v)}' for k, v in r.items() if k in ('dataTerminalReady', 'requestToSend'))
            print(f'{t:>6} SIG  {s}')
        elif ev == 'sleep':
            print(f'{t:>6} SLEEP {r["ms"]} ms')
        elif ev == 'open':
            print(f'{t:>6} OPEN {r["opts"]}')
        elif ev in ('close', 'readable-cancel', 'flash-written', 'done', 'error', 'progress'):
            print(f'{t:>6} {ev.upper()} {json.dumps({k: v for k, v in r.items() if k not in ("ev", "t")})}')
        elif ev == 'tx':
            raw = bytes.fromhex(r['hex'])
            p = unslip(raw)
            op, size, chk = p[1], struct.unpack_from('<H', p, 2)[0], struct.unpack_from('<I', p, 4)[0]
            data = p[8:]
            nw = min(len(data) // 4, 6)
            words = struct.unpack_from('<%dI' % nw, data) if nw else ()
            name = OPS.get(op, hex(op))
            extra = ''
            if op in (0x07, 0x11, 0x03):
                extra = f' len={words[0]} seq={words[1]} payload={len(data) - 16}B'
            elif op == 0x08:
                extra = ' payload=' + data[:4].hex() + '+55*' + str(len(data) - 4)
            else:
                extra = ' words=' + ','.join(f'0x{w:08x}' for w in words)
            rawhex = raw.hex() if (full or len(raw) <= 64) else raw[:24].hex() + '...'
            print(f'{t:>6} TX   {name:<17} size={size:<5} chk=0x{chk:02x}{extra}  wire({len(raw)})={rawhex}')
        elif ev == 'rx':
            raw = bytes.fromhex(r['hex'])
            if r['why'] in ('banner',):
                print(f'{t:>6} RX   banner {raw!r}')
            else:
                print(f'{t:>6} RX   {r["why"]:<17} wire({len(raw)})={raw.hex()}')


if __name__ == '__main__':
    main(sys.argv[1], full='--full' in sys.argv)
