package mysqlProxy

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"time"
)

/*
PacketHeader represents packet header
*/
type PacketHeader struct {
	Length     uint32
	SequenceId uint8
}

type FakeHandshakePacket struct {
	header                     *PacketHeader
	Protocol                   byte
	Version                    []byte
	ThreadId                   []byte
	Salt                       []byte
	ServerCapabilities         []byte
	ServerLanguage             byte
	ServerStatus               []byte
	ExtendedServerCapabilities []byte
	AuthenticationPluginLength byte
	Unused                     []byte
	Salt2                      []byte
	AuthenticationPlugin       []byte
}

func (f *FakeHandshakePacket) IncrementConnectID(curID []byte) {
	if curID == nil {
		f.ThreadId = []byte{0x01, 0x00, 0x00, 0x00}
		return
	}

	if curID[0] != 0xff {
		curID[0] += 1
		f.ThreadId = []byte{curID[0], curID[1], curID[2], curID[3]}
		return
	}
	if curID[1] != 0xff {
		curID[1] += 1
		f.ThreadId = []byte{curID[0], curID[1], curID[2], curID[3]}
		return
	}
	if curID[2] != 0xff {
		curID[2] += 1
		f.ThreadId = []byte{curID[0], curID[1], curID[2], curID[3]}
		return
	}
	if curID[3] != 0xff {
		curID[3] += 1
		f.ThreadId = []byte{curID[0], curID[1], curID[2], curID[3]}
		return
	} else {
		f.ThreadId = []byte{0x01, 0x00, 0x00, 0x00}
		return
	}
}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

func randStringRunes(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

var testSeed1 = "12345678"
var testSeed2 = "qwertyasdfgh"
var testSalt = false

func (r *FakeHandshakePacket) NewHandshakePacket(connID []byte) error {
	r.Protocol = byte(0x0a)

	r.Version = []byte("8.0.30")

	r.IncrementConnectID(connID)

	rand.Seed(time.Now().UnixNano())

	salt := make([]byte, 8)
	rand.Read(salt)
	r.Salt = []byte(randStringRunes(8))

	r.ServerCapabilities = []byte{0xff, 0xf7}

	r.ServerLanguage = byte(0xff)

	r.ServerStatus = []byte{0x02, 0x00}

	r.ExtendedServerCapabilities = []byte{0xff, 0xdf}

	r.AuthenticationPluginLength = byte(0x15)

	r.Unused = []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}

	salt2 := make([]byte, 12)
	rand.Read(salt2)
	r.Salt2 = []byte(randStringRunes(12))

	r.AuthenticationPlugin = []byte("mysql_native_password")

	return nil
}

func (r *FakeHandshakePacket) Encode() ([]byte, error) {
	buf := make([]byte, 0)
	buf = append(buf, r.Protocol)
	buf = append(buf, r.Version...)
	buf = append(buf, byte(0x00))

	buf = append(buf, r.ThreadId...)

	buf = append(buf, r.Salt...)
	buf = append(buf, byte(0x00))

	buf = append(buf, r.ServerCapabilities...)

	buf = append(buf, r.ServerLanguage)

	buf = append(buf, r.ServerStatus...)

	buf = append(buf, r.ExtendedServerCapabilities...)

	buf = append(buf, r.AuthenticationPluginLength)

	buf = append(buf, r.Unused...)

	buf = append(buf, r.Salt2...)
	buf = append(buf, byte(0x00))

	buf = append(buf, r.AuthenticationPlugin...)

	r.header = &PacketHeader{
		Length:     uint32(len(buf)),
		SequenceId: 0,
	}

	newBuf := make([]byte, 0, r.header.Length+4)

	ln := make([]byte, 4)
	binary.LittleEndian.PutUint32(ln, r.header.Length)

	newBuf = append(newBuf, ln[:3]...)
	newBuf = append(newBuf, r.header.SequenceId)
	newBuf = append(newBuf, buf...)

	return newBuf, nil
}

/*
InitialHandshakePacket represents initial handshake packet sent by MySQL Server
*/
type InitialHandshakePacket struct {
	ProtocolVersion   uint8
	ServerVersion     []byte
	ConnectionId      uint32
	AuthPluginData    []byte
	Filler            byte
	CapabilitiesFlags CapabilityFlag
	CharacterSet      uint8
	StatusFlags       uint16
	AuthPluginDataLen uint8
	AuthPluginName    []byte
	header            *PacketHeader
}

type AuthorizationPacket struct {
	header      *PacketHeader
	PacketPart1 []byte
	Username    []byte
	Password    []byte
	CapData     []byte
	CapData2    []byte
	PacketPart2 []byte
}

func (r *AuthorizationPacket) Decode(conn net.Conn) error {

	data := make([]byte, 1024)
	_, err := conn.Read(data)
	if err != nil {
		return err
	}

	header := &PacketHeader{}
	ln := []byte{data[0], data[1], data[2], 0x00}
	header.Length = binary.LittleEndian.Uint32(ln)
	header.SequenceId = data[3]
	r.header = header

	payload := data[4 : header.Length+4]

	position := 0

	r.PacketPart1 = payload[position : position+32]

	position += 32

	for _, element := range payload[position:] {
		r.Username = append(r.Username, element)
		if element == 0x00 {
			break
		}
	}

	position += len(r.Username) + 1

	r.Password = payload[position : position+20]

	position += 20

	for _, element := range payload[position:] {
		r.CapData = append(r.CapData, element)
		if element == 0x00 {
			break
		}
	}

	position += len(r.CapData)

	r.PacketPart2 = payload[position:]

	//fmt.Printf("\n\n--------------------------Decode--------------------------\n\n")
	//dumpByteSlice(data[:header.Length+4])

	return nil
}

func (r AuthorizationPacket) Encode() ([]byte, error) {
	buf := make([]byte, 0)
	buf = append(buf, r.PacketPart1...)
	buf = append(buf, r.Username...)
	buf = append(buf, 0x00)
	buf = append(buf, byte(len(r.Password)))
	buf = append(buf, r.Password...)
	buf = append(buf, r.CapData...)
	buf = append(buf, r.PacketPart2...)

	h := PacketHeader{
		Length:     uint32(len(buf)),
		SequenceId: r.header.SequenceId,
	}

	newBuf := make([]byte, 0, h.Length+4)

	ln := make([]byte, 4)
	binary.LittleEndian.PutUint32(ln, h.Length)

	newBuf = append(newBuf, ln[:3]...)
	newBuf = append(newBuf, h.SequenceId)
	newBuf = append(newBuf, buf...)

	//fmt.Printf("\n\n--------------------------Encode--------------------------\n\n")
	//dumpByteSlice(newBuf)

	return newBuf, nil

}

// Decode decodes the first packet received from the MySQl Server
// It's a handshake packet
func (r *InitialHandshakePacket) Decode(conn net.Conn) error {
	data := make([]byte, 1024)
	_, err := conn.Read(data)
	if err != nil {
		return err
	}

	header := &PacketHeader{}
	ln := []byte{data[0], data[1], data[2], 0x00}
	header.Length = binary.LittleEndian.Uint32(ln)
	// a single byte integer is the same in BigEndian and LittleEndian
	header.SequenceId = data[3]

	r.header = header
	/**
	Assign payload only data to new var just  for convenience
	*/
	payload := data[4 : header.Length+4]
	position := 0
	/**
	As defined in the documentation, this value is alway 10 (0x00 in hex)
	1	[0a] protocol version
	*/
	r.ProtocolVersion = payload[0]
	if r.ProtocolVersion != 0x0a {
		return errors.New("non supported protocol for the proxy. Only version 10 is supported")
	}

	position += 1

	/**
	Extract server version, by finding the terminal character (0x00) index,
	and extracting the data in between
	string[NUL]    server version
	*/
	index := bytes.IndexByte(payload, byte(0x00))
	r.ServerVersion = payload[position:index]
	position = index + 1

	connectionId := payload[position : position+4]
	id := binary.LittleEndian.Uint32(connectionId)
	r.ConnectionId = id
	position += 4

	/*
		The auth-plugin-data is the concatenation of strings auth-plugin-data-part-1 and auth-plugin-data-part-2.
	*/

	r.AuthPluginData = make([]byte, 8)
	copy(r.AuthPluginData, payload[position:position+8])
	position += 8
	r.Filler = payload[position]
	if r.Filler != 0x00 {
		return errors.New("failed to decode filler value")
	}

	position += 1

	capabilitiesFlags1 := payload[position : position+2]
	position += 2

	r.CharacterSet = payload[position]
	position += 1

	r.StatusFlags = binary.LittleEndian.Uint16(payload[position : position+2])
	position += 2

	capabilityFlags2 := payload[position : position+2]
	position += 2

	/**
	Reconstruct 32 bit integer from two 16 bit integers.
	Take low 2 bytes and high 2 bytes, ans sum it.
	*/
	capLow := binary.LittleEndian.Uint16(capabilitiesFlags1)
	capHi := binary.LittleEndian.Uint16(capabilityFlags2)
	cap := uint32(capLow) | uint32(capHi)<<16

	r.CapabilitiesFlags = CapabilityFlag(cap)

	if r.CapabilitiesFlags&clientPluginAuth != 0 {
		r.AuthPluginDataLen = payload[position]
		if r.AuthPluginDataLen == 0 {
			return errors.New("wrong auth plugin data len")
		}
	}

	/*
		Skip reserved bytes

		string[10]     reserved (all [00])
	*/

	position += 1 + 10

	/**
	This flag tell us that the client should hash the password using algorithm described here:
	https://dev.mysql.com/doc/internals/en/secure-password-authentication.html#packet-Authentication::Native41
	*/
	if r.CapabilitiesFlags&clientSecureConn != 0 {
		/*
			The auth-plugin-data is the concatenation of strings auth-plugin-data-part-1 and auth-plugin-data-part-2.
		*/
		end := position + Max(13, int(r.AuthPluginDataLen)-8)
		r.AuthPluginData = append(r.AuthPluginData, payload[position:end]...)
		position = end
	}

	index = bytes.IndexByte(payload[position:], byte(0x00))

	/*
		Due to Bug#59453 the auth-plugin-name is missing the terminating NUL-char in versions prior to 5.5.10 and 5.6.2.
		We know the length of the payload, so if there is no NUL-char, just read all the data until the end
	*/
	if index != -1 {
		r.AuthPluginName = payload[position : position+index]
	} else {
		r.AuthPluginName = payload[position:]
	}

	return nil
}

// Encode encodes the InitialHandshakePacket to bytes
func (r InitialHandshakePacket) Encode() ([]byte, error) {
	buf := make([]byte, 0)
	buf = append(buf, r.ProtocolVersion)
	buf = append(buf, r.ServerVersion...)
	buf = append(buf, byte(0x00))

	connectionId := make([]byte, 4)
	binary.LittleEndian.PutUint32(connectionId, r.ConnectionId)
	buf = append(buf, connectionId...)

	//auth1 := make([]byte, 8)
	auth1 := r.AuthPluginData[0:8]
	buf = append(buf, auth1...)
	buf = append(buf, 0x00)

	cap := make([]byte, 4)
	binary.LittleEndian.PutUint32(cap, uint32(r.CapabilitiesFlags))

	cap1 := cap[0:2]
	cap2 := cap[2:]

	buf = append(buf, cap1...)
	buf = append(buf, r.CharacterSet)

	statusFlag := make([]byte, 2)
	binary.LittleEndian.PutUint16(statusFlag, r.StatusFlags)
	buf = append(buf, statusFlag...)
	buf = append(buf, cap2...)
	buf = append(buf, r.AuthPluginDataLen)

	reserved := make([]byte, 10)
	buf = append(buf, reserved...)
	buf = append(buf, r.AuthPluginData[8:]...)
	buf = append(buf, r.AuthPluginName...)
	buf = append(buf, 0x00)

	h := PacketHeader{
		Length:     uint32(len(buf)),
		SequenceId: r.header.SequenceId,
	}

	newBuf := make([]byte, 0, h.Length+4)

	ln := make([]byte, 4)
	binary.LittleEndian.PutUint32(ln, h.Length)

	newBuf = append(newBuf, ln[:3]...)
	newBuf = append(newBuf, h.SequenceId)
	newBuf = append(newBuf, buf...)

	return newBuf, nil
}

func (r InitialHandshakePacket) String() string {
	return r.CapabilitiesFlags.String()
}

func ScramblePassword(scramble []byte, password string) []byte {
	if len(password) == 0 {
		return nil
	}

	// stage1Hash = SHA1(password)
	crypt := sha1.New()
	crypt.Write([]byte(password))
	stage1 := crypt.Sum(nil)

	// scrambleHash = SHA1(scramble + SHA1(stage1Hash))
	// inner Hash
	crypt.Reset()
	crypt.Write(stage1)
	hash := crypt.Sum(nil)

	// outer Hash
	crypt.Reset()
	crypt.Write(scramble)
	crypt.Write(hash)
	scramble = crypt.Sum(nil)

	// token = scrambleHash XOR stage1Hash
	for i := range scramble {
		scramble[i] ^= stage1[i]
	}
	return scramble
}

func dumpByteSlice(b []byte) {
	var a [16]byte
	n := (len(b) + 15) &^ 15
	for i := 0; i < n; i++ {
		if i%16 == 0 {
			fmt.Printf("%4d", i)
		}
		if i%8 == 0 {
			fmt.Print(" ")
		}
		if i < len(b) {
			fmt.Printf(" %02X", b[i])
		} else {
			fmt.Print("   ")
		}
		if i >= len(b) {
			a[i%16] = ' '
		} else if b[i] < 32 || b[i] > 126 {
			a[i%16] = '.'
		} else {
			a[i%16] = b[i]
		}
		if i%16 == 15 {
			fmt.Printf("  %s\n", string(a[:]))
		}
	}
}
