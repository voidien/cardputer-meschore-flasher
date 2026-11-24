# Prints a recording (.jsonl or .jsonl.gz) as a listing of frames and sleeps, for reading it. Run with python3 -I:
#   python3 -I summarize.py ../../internal/nrfdfu/testdata/tiny-1028.jsonl
# From the research prototype (proto-nrf52/summarize.py), reading gzipped recordings too.
import gzip, json, sys
path = sys.argv[1]
with (gzip.open(path, 'rt') if path.endswith('.gz') else open(path)) as f:
    ev = [json.loads(l) for l in f if l.strip()]
def unslip(h):
    b=bytes.fromhex(h); assert b[0]==0xC0 and b[-1]==0xC0
    o=bytearray(); i=1
    while i<len(b)-1:
        if b[i]==0xDB: o.append(0xC0 if b[i+1]==0xDC else 0xDB); i+=2
        else: o.append(b[i]); i+=1
    return bytes(o)
names={1:'INIT',3:'START',4:'DATA',5:'STOP',6:'ERASE'}
out=[]; dcount=0
for e in ev:
    if e['t']=='write':
        f=unslip(e['hex']); seq=f[0]&7; ack=(f[0]>>3)&7; L=(f[1]>>4)|(f[2]<<4); typ=f[4]
        nm=names.get(typ,str(typ))
        if nm=='DATA':
            dcount+=1
            out.append(f"W DATA#{dcount} seq={seq} ack={ack} len={L} wire={e['len']}")
        else:
            out.append(f"W {nm} seq={seq} ack={ack} len={L} wire={e['len']} hdr={f[:4].hex()} payload={f[4:-2].hex()} crc={f[-2:].hex()} raw={e['hex']}")
    else:
        out.append(f"{e['t'].upper()} " + json.dumps({k:v for k,v in e.items() if k not in ('t','wall_ms')}))
for l in out[:30]: print(l)
if len(out) > 44:
    print('...')
for l in out[max(30, len(out)-14):]: print(l)
# where the sleeps are relative to the DATA frames
pos=[]; d=0
for e in ev:
    if e['t']=='write':
        f=unslip(e['hex'])
        if f[4]==4: d+=1
    if e['t']=='sleep': pos.append((d,e['ms']))
print('sleep positions (DATA frames sent before the sleep, ms):', pos[:5], '...', pos[-4:])
print('DATA frames', d)
