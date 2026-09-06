// Copyright (c) the go-xreal authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package air

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

// TestTheChecksumIsPlainCRC32.
//
// ⛔ THE REFERENCE CALLS IT "crc32_adler" AND THAT NAME IS WRONG. Adler-32 is a
// different algorithm entirely -- two running sums mod 65521, no table. What
// the reference actually computes is a reflected table starting at all ones and
// inverted at the end, which is CRC-32/ISO-HDLC. This pins the reading against
// a table that is NOT the standard library's own, so the claim is checked
// rather than assumed.
func TestTheChecksumIsPlainCRC32(t *testing.T) {
	// The reference algorithm, written out: register at all ones, reflected
	// polynomial 0xEDB88320, inverted at the end.
	ref := func(b []byte) uint32 {
		r := uint32(0xFFFFFFFF)
		for _, c := range b {
			r ^= uint32(c)
			for i := 0; i < 8; i++ {
				if r&1 != 0 {
					r = (r >> 1) ^ 0xEDB88320
				} else {
					r >>= 1
				}
			}
		}
		return r ^ 0xFFFFFFFF
	}
	for _, in := range [][]byte{
		nil, {0}, {'a'}, []byte("123456789"), bytes.Repeat([]byte{0xFD}, 64),
	} {
		if got, want := CRC(in), ref(in); got != want {
			t.Errorf("CRC(% x) = %#08x, the reference gives %#08x", in, got, want)
		}
	}
	// And the classic check value, so a broken standard library would show.
	if got := CRC([]byte("123456789")); got != 0xCBF43926 {
		t.Errorf(`CRC("123456789") = %#08x, want the CRC-32 check value`, got)
	}
}

// TestACommandIsBuiltWhereTheReferenceBuildsIt.
func TestACommandIsBuiltWhereTheReferenceBuildsIt(t *testing.T) {
	b, err := Command(0x0203, 0, []byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != PacketSize {
		t.Fatalf("the packet is %d bytes, want %d", len(b), PacketSize)
	}
	if b[0] != MCUHead {
		t.Errorf("it begins %#02x", b[0])
	}
	if got := binary.LittleEndian.Uint16(b[5:7]); got != 3+17 {
		t.Errorf("length is %d, want payload+17", got)
	}
	if got := binary.LittleEndian.Uint32(b[7:11]); got != RequestID {
		t.Errorf("request id is %#x", got)
	}
	if got := binary.LittleEndian.Uint16(b[15:17]); got != 0x0203 {
		t.Errorf("command id is %#x", got)
	}
	if !bytes.Equal(b[22:25], []byte{1, 2, 3}) {
		t.Errorf("the payload landed at % x", b[22:25])
	}
	// ⛔ THE CHECKSUM COVERS FROM BYTE 5 AND EXCLUDES ITSELF. Getting that
	// range wrong produces a packet that looks right and is refused, which is
	// the least debuggable kind of wrong.
	want := CRC(b[5 : 5+3+17])
	if got := binary.LittleEndian.Uint32(b[1:5]); got != want {
		t.Errorf("checksum %#08x, want %#08x over bytes 5..%d", got, want, 5+3+17)
	}
}

// TestACommandAndItsReplyAgree, which is the only round trip available without
// a headset.
//
// ⚠ AND IT IS NOT A DEVICE CONFIRMING ANYTHING. This says the writer and the
// reader here agree with each other; it cannot say either agrees with a pair
// of glasses. Nothing in this package has been answered by one yet.
func TestACommandAndItsReplyAgree(t *testing.T) {
	b, err := Command(0x1234, 0xDEADBEEF, []byte{9, 8, 7, 6})
	if err != nil {
		t.Fatal(err)
	}
	r, err := ParseReply(b)
	if err != nil {
		t.Fatal(err)
	}
	if r.CommandID != 0x1234 {
		t.Errorf("command id came back %#x", r.CommandID)
	}
	if r.Timestamp != 0xDEADBEEF {
		t.Errorf("timestamp came back %#x", r.Timestamp)
	}
	if !bytes.Equal(r.Payload, []byte{9, 8, 7, 6}) {
		t.Errorf("payload came back % x", r.Payload)
	}
	// An empty payload is a whole packet too.
	b, _ = Command(1, 0, nil)
	if r, err := ParseReply(b); err != nil || len(r.Payload) != 0 {
		t.Errorf("an empty command round-tripped to %v, %v", r, err)
	}
}

// TestAPayloadThatDoesNotFitIsRefused rather than truncated.
func TestAPayloadThatDoesNotFitIsRefused(t *testing.T) {
	if _, err := Command(1, 0, make([]byte, MaxPayload+1)); !errors.Is(err, ErrTooLong) {
		t.Errorf("an oversized payload = %v", err)
	}
	if _, err := Command(1, 0, make([]byte, MaxPayload)); err != nil {
		t.Errorf("a payload that exactly fits = %v", err)
	}
}

// TestTheTwoPacketKindsAreToldApart.
//
// ⛔ THEY COUNT THEIR LENGTHS DIFFERENTLY. An MCU packet's length is payload
// plus seventeen; an IMU packet's includes the command id and two checksum
// bytes, so its payload is three SHORTER than it says. Two kinds on one device,
// each counting its own way.
func TestTheTwoPacketKindsAreToldApart(t *testing.T) {
	mcu, _ := Command(1, 0, []byte{1})
	if _, err := ParseIMU(mcu); !errors.Is(err, ErrNotIMU) {
		t.Errorf("an MCU packet parsed as IMU: %v", err)
	}

	imu := make([]byte, 32)
	imu[0] = IMUHead
	binary.LittleEndian.PutUint16(imu[5:7], 3+4) // command id + 2 checksum + 4 data
	imu[7] = 0x55
	copy(imu[IMUHeaderSize:], []byte{1, 2, 3, 4})
	p, err := ParseIMU(imu)
	if err != nil {
		t.Fatal(err)
	}
	if p.CommandID != 0x55 {
		t.Errorf("IMU command id %#02x", p.CommandID)
	}
	if !bytes.Equal(p.Payload, []byte{1, 2, 3, 4}) {
		t.Errorf("IMU payload % x", p.Payload)
	}
	if _, err := ParseReply(imu); !errors.Is(err, ErrNotMCU) {
		t.Errorf("an IMU packet parsed as MCU: %v", err)
	}
}

// TestAPacketShorterThanItClaimsIsRefused, in both kinds.
func TestAPacketShorterThanItClaimsIsRefused(t *testing.T) {
	b := make([]byte, 24)
	b[0] = MCUHead
	binary.LittleEndian.PutUint16(b[5:7], 200)
	if _, err := ParseReply(b); !errors.Is(err, ErrShort) {
		t.Errorf("an overlong MCU length = %v", err)
	}
	binary.LittleEndian.PutUint16(b[5:7], 3)
	if _, err := ParseReply(b); !errors.Is(err, ErrShort) {
		t.Errorf("an MCU length below the header = %v", err)
	}
	i := make([]byte, 10)
	i[0] = IMUHead
	binary.LittleEndian.PutUint16(i[5:7], 200)
	if _, err := ParseIMU(i); !errors.Is(err, ErrShort) {
		t.Errorf("an overlong IMU length = %v", err)
	}
	binary.LittleEndian.PutUint16(i[5:7], 1)
	if _, err := ParseIMU(i); !errors.Is(err, ErrShort) {
		t.Errorf("an IMU length below its own overhead = %v", err)
	}
	if _, err := ParseReply(nil); !errors.Is(err, ErrNotMCU) {
		t.Errorf("nothing = %v", err)
	}
	if _, err := ParseIMU(nil); !errors.Is(err, ErrNotIMU) {
		t.Errorf("nothing = %v", err)
	}
}

// TestTheAir2UltraIsOnADifferentInterface.
//
// ⛔ THREE HEADSETS ANSWER ON INTERFACE 4 AND ONE ANSWERS ON 0. A caller that
// assumes the common case works on three and fails silently on the fourth.
func TestTheAir2UltraIsOnADifferentInterface(t *testing.T) {
	for _, p := range []uint16{ProductAir, ProductAir2, ProductAir2Pro} {
		if got := MCUInterface(p); got != 4 {
			t.Errorf("product %#04x is on interface %d, want 4", p, got)
		}
	}
	if got := MCUInterface(ProductAir2Ultra); got != 0 {
		t.Errorf("the Air 2 Ultra is on interface %d, want 0", got)
	}
}
