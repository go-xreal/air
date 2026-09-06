# go-xreal/air

The **XREAL Air** family's USB packet protocol in pure Go, `CGO_ENABLED=0`.

```go
b, err := air.Command(0x0203, 0, []byte{1, 2, 3})   // an MCU packet
r, err := air.ParseReply(b)                          // and back
p, err := air.ParseIMU(sensorBytes)                  // a sensor report
```

## ⚠ It has NOT been run against a headset

Every fact here comes from **reading a working implementation**. The tests check
the model against itself and against Go's own CRC. **Nothing has yet been
confirmed by a device answering.**

⛔ A port nobody has plugged in is a translation, not a driver. That is why
there is no `Open` in this package yet: shipping a transport that has never
carried a byte would put a confident face on an untested thing.

## ⛔ This protocol was not reverse engineered here

Unlike VITURE's, it is already free. This is a **port, with attribution**:

- [ar-drivers-rs](https://github.com/badicsalex/ar-drivers-rs) — MIT, © 2023 Alex Badics
- itself based on [nrealAirLinuxDriver](https://gitlab.com/TheJackiMonster/nrealAirLinuxDriver) by TheJackiMonster

Porting something that already works is a different job from decoding something
nobody has, and saying which one you did is part of doing it.

## The packets

An **MCU** packet, 64 bytes, in both directions:

```
0     head       0xFD
1-4   checksum   CRC-32 over bytes 5 .. 5+length
5-6   length     len(payload) + 17
7-10  requestID  always 0x1337
11-14 timestamp
15-16 commandID
17-21 reserved
22..  payload    up to 42 bytes
```

⛔ **The checksum covers from byte 5 and excludes itself.** Getting that range
wrong produces a packet that looks right and is refused — the least debuggable
kind of wrong.

⚠ **`requestID` never changes.** A field with that name that is always `0x1337`
cannot be what pairs a reply with its question. Copied as found rather than
improved: a device that ignores a field and a device that demands this exact
value look identical until one is tried.

An **IMU** packet counts differently:

```
0     head       0xAA
1-4   checksum
5-6   length     command id (1) + checksum (2) + payload
7     commandID
8..   payload    length - 3
```

⛔ Two packet kinds on one device, **each counting its own way**. That is
exactly the shape that reads as an off-by-three at three in the morning.

## ⭐ The checksum is plain CRC-32, despite its name upstream

The reference calls it `crc32_adler`. **Adler-32 is a different algorithm** —
two running sums mod 65521, no table. What the reference computes is a
reflected table, register starting at all ones, inverted at the end:
CRC-32/ISO-HDLC exactly. So this is one line of the standard library, and a
test pins the equivalence against an independently written implementation
rather than trusting the reading.

## ⚠ The Air 2 Ultra is on a different USB interface

Interface **4** for the Air, Air 2 and Air 2 Pro; interface **0** for the Air 2
Ultra. A caller that assumes the common case works on three headsets and fails
silently on the fourth.

## Licence

BSD-3-Clause.
