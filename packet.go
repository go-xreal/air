// Copyright (c) the go-xreal authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

// Package air speaks the XREAL Air family's USB protocol in pure Go, with
// CGO_ENABLED=0.
//
// ⛔ THIS PROTOCOL WAS NOT REVERSE ENGINEERED HERE, and that is the point.
// Unlike VITURE's, it is already free: ar-drivers-rs (MIT, Alex Badics), itself
// based on TheJackiMonster's nrealAirLinuxDriver. This package is a PORT, with
// attribution, not a rediscovery -- and porting something that already works is
// a different job from decoding something nobody has.
//
// ⚠ AND IT HAS NOT BEEN RUN AGAINST A HEADSET. Every fact here comes from
// reading a working implementation; the tests check the model against itself
// and against Go's own CRC. Nothing has yet been confirmed by a device
// answering. A port nobody has plugged in is a translation, and the README says
// so where somebody will see it.
package air

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// The devices this speaks to.
//
// ⭐ From ar-drivers-rs, which distinguishes the models by product id -- and the
// model matters, because the MCU is on a different USB interface for the Air 2
// Ultra than for the rest.
const (
	VendorID uint16 = 0x3318

	ProductAir       uint16 = 0x0424
	ProductAir2      uint16 = 0x0428
	ProductAir2Pro   uint16 = 0x0432
	ProductAir2Ultra uint16 = 0x0426
)

// MCUInterface is which USB interface carries commands, per model.
//
// ⚠ THE AIR 2 ULTRA IS THE ODD ONE. The others answer on interface 4; it
// answers on 0. A caller that assumes one number works on three headsets and
// silently fails on the fourth.
func MCUInterface(product uint16) int {
	if product == ProductAir2Ultra {
		return 0
	}
	return 4
}

// PacketSize is how many bytes an MCU report carries, in both directions.
const PacketSize = 0x40

// MaxPayload is how much of it is available to a command.
//
// The header takes 22 of the 64 bytes; what is left is the payload.
const MaxPayload = 42

// Frame markers.
const (
	// MCUHead begins every command packet and every reply to one.
	MCUHead byte = 0xFD
	// IMUHead begins a sensor packet, which is a different shape entirely.
	IMUHead byte = 0xAA
)

// RequestID is what the reference implementation puts in every command.
//
// ⚠ A CONSTANT WHERE A COUNTER WOULD BE EXPECTED. The field is called a request
// id and it never changes, so it cannot be what pairs a reply with its
// question. Copied as found rather than improved: a device that ignores a field
// and a device that requires this exact value look identical until one is
// tried, and neither has been.
const RequestID uint32 = 0x1337

// Errors this package returns.
var (
	// ErrNotMCU says a buffer did not begin with [MCUHead].
	ErrNotMCU = errors.New("air: not an MCU packet")
	// ErrNotIMU says a buffer did not begin with [IMUHead].
	ErrNotIMU = errors.New("air: not an IMU packet")
	// ErrShort says a packet claims more bytes than it carries.
	ErrShort = errors.New("air: the packet is shorter than its length says")
	// ErrTooLong says a payload does not fit in a packet.
	ErrTooLong = errors.New("air: the payload does not fit in one packet")
)

// CRC is the checksum both packet kinds carry.
//
// ⛔ IT IS PLAIN CRC-32, DESPITE ITS NAME UPSTREAM. The reference calls it
// "crc32_adler", and Adler-32 is a different algorithm entirely -- but the
// table is the reflected CRC-32 one, the register starts at all ones and the
// result is inverted, which is CRC-32/ISO-HDLC exactly. So this is one line of
// the standard library instead of a copied table, and a test pins the
// equivalence rather than trusting the reading.
func CRC(b []byte) uint32 { return crc32.ChecksumIEEE(b) }

// Command builds an MCU packet.
//
//	0    head      0xFD
//	1-4  checksum  CRC-32 over bytes 5 .. 5+length
//	5-6  length    len(payload) + 17
//	7-10 requestID
//	11-14 timestamp
//	15-16 commandID
//	17-21 reserved
//	22..  payload
func Command(commandID uint16, timestamp uint32, payload []byte) ([]byte, error) {
	if len(payload) > MaxPayload {
		return nil, fmt.Errorf("%w: %d bytes, room for %d", ErrTooLong, len(payload), MaxPayload)
	}
	b := make([]byte, PacketSize)
	b[0] = MCUHead
	length := uint16(len(payload)) + 17
	binary.LittleEndian.PutUint16(b[5:7], length)
	binary.LittleEndian.PutUint32(b[7:11], RequestID)
	binary.LittleEndian.PutUint32(b[11:15], timestamp)
	binary.LittleEndian.PutUint16(b[15:17], commandID)
	copy(b[22:], payload)
	// ⛔ THE CHECKSUM COVERS FROM BYTE 5, NOT FROM THE START, and it is written
	// after everything else it covers. Its own four bytes are excluded, which
	// is why it can be filled in last.
	binary.LittleEndian.PutUint32(b[1:5], CRC(b[5:5+int(length)]))
	return b, nil
}

// Reply is an MCU packet the headset sent.
type Reply struct {
	CommandID uint16
	Timestamp uint32
	Payload   []byte
}

// ParseReply splits an MCU packet.
func ParseReply(b []byte) (Reply, error) {
	if len(b) < 22 || b[0] != MCUHead {
		return Reply{}, fmt.Errorf("%w: % x", ErrNotMCU, head(b))
	}
	length := int(binary.LittleEndian.Uint16(b[5:7]))
	if length < 17 || 22+length-17 > len(b) {
		return Reply{}, fmt.Errorf("%w: length %d in %d bytes", ErrShort, length, len(b))
	}
	return Reply{
		CommandID: binary.LittleEndian.Uint16(b[15:17]),
		Timestamp: binary.LittleEndian.Uint32(b[11:15]),
		Payload:   b[22 : 22+length-17],
	}, nil
}

// IMUHeaderSize is how many bytes precede a sensor payload.
const IMUHeaderSize = 8

// IMUPacket is one sensor report.
type IMUPacket struct {
	CommandID byte
	Payload   []byte
}

// ParseIMU splits a sensor packet.
//
// ⚠ ITS LENGTH COUNTS DIFFERENTLY FROM AN MCU PACKET'S: it includes the command
// id and two checksum bytes, so the payload is three shorter than it says.
// Two packet kinds on one device, each counting its own way, is exactly the
// sort of thing that reads as an off-by-three at three in the morning.
func ParseIMU(b []byte) (IMUPacket, error) {
	if len(b) < IMUHeaderSize || b[0] != IMUHead {
		return IMUPacket{}, fmt.Errorf("%w: % x", ErrNotIMU, head(b))
	}
	length := int(binary.LittleEndian.Uint16(b[5:7]))
	n := length - 3
	if n < 0 || IMUHeaderSize+n > len(b) {
		return IMUPacket{}, fmt.Errorf("%w: length %d in %d bytes", ErrShort, length, len(b))
	}
	return IMUPacket{CommandID: b[7], Payload: b[IMUHeaderSize : IMUHeaderSize+n]}, nil
}

// head is the first few bytes of a buffer, for an error message.
func head(b []byte) []byte {
	if len(b) > 8 {
		return b[:8]
	}
	return b
}
