# Wire Format Notes

Corrections and non-obvious behaviors discovered during live interop testing against `zenohd` (Rust router, protocol `0x09`). These are not documented in the zenoh-pico source comments and were found empirically.

## 1. Cookie encoding in `InitAck`

**Expected (per research):** cookie is u16-LE length-prefixed  
**Actual:** cookie is **varint (LEB128) length-prefixed**

The Rust zenoh-codec uses `Zenoh080Bounded::<u16>` for the cookie, which means the length is a varint bounded to u16 range — not a raw 2-byte LE field.

Evidence from a live `InitAck` (72 bytes):
```
...0a 00c0 31 30b157...bec5...
       ^^^^  ^^
       batchsize (u16 LE = 49152)
              varint 0x31 = 49 (cookie length)
```

**Fix:** `ReadCookie` and `AppendCookie` use `ReadUvarint`/`AppendUvarint`.

---

## 2. `InitSyn` S flag and `InitAck` S flag

**Rule:** the S flag (size params present) in `InitSyn` should only be set when proposing **non-default** resolution or batch_size.

From the Rust client source:
```rust
if resolution != &Resolution::default() || batch_size != &batch_size::UNICAST {
    header |= flag::S;
}
```

Defaults: `Resolution = 0x0a` (SN:U32 | ReqID:U32), `BatchSize::UNICAST = 65535`.

When the client sends S=0 (defaults), `zenohd` may respond with S=0 in `InitAck` (omitting resolution+batchSize). The decoder must check the S flag from the `InitAck` header before attempting to read those fields.

**Fix:** `decodeInitAck(r, hdrFlags, hasZ)` — conditionally reads resolution+batchSize based on `hdrFlags & FlagS`.

---

## 3. Put body timestamp (T flag = `0x20`)

**Symptom:** `makeslice: len out of range` panic in `ReadZBytes` when receiving a forwarded Put.

**Cause:** `zenohd` adds a **timestamp** to every Put it forwards to subscribers. The Put body header has the T flag (`0x20`) set. Our decoder did not know about this flag and tried to read the timestamp bytes as the payload length varint, producing an astronomically large value.

**Timestamp wire format** (when T flag set in Put/Del/Reply body header):
```
[NTP u64 as varint]   — NTP timestamp (9 bytes for current epoch)
[ZID as zbytes]       — varint length + ZID bytes (router's ZID)
```

Evidence from a captured Put body (`0x21` header = ZidPut | T):
```
21 f0fc82c8e691c4ef69 10 688d...bec5 18 68656c6c6f...
^^ ^^^^^^^^^^^^^^^^^ ^^ ^^^^^^^^^^^^ ^^ ^^^^^^^^^^^^^^
hdr  NTP ts (9 bytes) 16  ZID (16B)  24  payload
```

**Fix:** `DecodePutBody`, `DecodeDelBody`, `DecodeReplyBody` — skip timestamp (`skipTimestamp(r)`) when `flags & BodyFlagT != 0`.

---

## 4. WhatAmI encoding: two different formats

| Context             | Router | Peer | Client |
| ------------------- | ------ | ---- | ------ |
| Scout `what` field  | 0x01   | 0x02 | 0x04   |
| INIT/HELLO body     | 0x00   | 0x01 | 0x02   |

The Scout `what` field uses a **bitmask** (combinable). INIT and HELLO use a **2-bit index** (exclusive role). These must never be mixed.

---

## 5. Frame payload decoding

Network messages inside a Frame are **self-describing** — no length prefix, no count field. The receiver reads from the exact-size buffer (bounded by the TCP 2-byte length prefix) until the buffer is exhausted. `DecodeNetworkStream` loops until `io.EOF`.

---

## 6. Session close ordering

To avoid dropping outbound frames on `Close()`:

1. Enqueue the CLOSE network message to `writeCh`
2. `close(writeCh)` — signals writerLoop to drain remaining frames
3. Wait for `writerDone` — all frames (including CLOSE) are now on the wire
4. `transport.Close()` — unblocks the blocked `readerLoop`
5. `cancel()` — stops keepalive and watchdog goroutines
6. `wg.Wait()` — wait for all goroutines to exit

Closing the transport before the writer finishes causes `Put` frames to be silently dropped.
