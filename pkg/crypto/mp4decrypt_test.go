package crypto

import (
	"bytes"
	"testing"
)

func TestHexToBytes(t *testing.T) {
	tests := []struct {
		name    string
		hex     string
		want    []byte
		wantErr bool
	}{
		{
			name: "valid 16-byte key",
			hex:  "00112233445566778899aabbccddeeff",
			want: []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
		},
		{
			name: "valid short hex",
			hex:  "abcd",
			want: []byte{0xab, 0xcd},
		},
		{
			name:    "odd length",
			hex:     "abc",
			wantErr: true,
		},
		{
			name: "empty string",
			hex:  "",
			want: []byte{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := hexToBytes(tt.hex)
			if (err != nil) != tt.wantErr {
				t.Errorf("hexToBytes() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !bytes.Equal(got, tt.want) {
				t.Errorf("hexToBytes() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPackAtom(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03, 0x04}
	result := packAtom("test", data)

	// Size should be 8 (header) + 4 (data) = 12
	if len(result) != 12 {
		t.Errorf("packAtom() length = %d, want 12", len(result))
	}

	// Check size field (big endian)
	if result[0] != 0 || result[1] != 0 || result[2] != 0 || result[3] != 12 {
		t.Errorf("packAtom() size = %v, want [0 0 0 12]", result[:4])
	}

	// Check type field
	if string(result[4:8]) != "test" {
		t.Errorf("packAtom() type = %s, want 'test'", string(result[4:8]))
	}

	// Check data
	if !bytes.Equal(result[8:], data) {
		t.Errorf("packAtom() data = %v, want %v", result[8:], data)
	}
}

func TestParseAtoms(t *testing.T) {
	// Build a simple atom: size(4) + type(4) + data
	atom1 := packAtom("ftyp", []byte{0x01, 0x02})
	atom2 := packAtom("moov", []byte{0x03, 0x04, 0x05})
	combined := append(atom1, atom2...)

	atoms := parseAtoms(combined)

	if len(atoms) != 2 {
		t.Fatalf("parseAtoms() got %d atoms, want 2", len(atoms))
	}

	if atoms[0].atomType != "ftyp" {
		t.Errorf("atoms[0].atomType = %s, want 'ftyp'", atoms[0].atomType)
	}
	if !bytes.Equal(atoms[0].data, []byte{0x01, 0x02}) {
		t.Errorf("atoms[0].data = %v, want [1 2]", atoms[0].data)
	}

	if atoms[1].atomType != "moov" {
		t.Errorf("atoms[1].atomType = %s, want 'moov'", atoms[1].atomType)
	}
	if !bytes.Equal(atoms[1].data, []byte{0x03, 0x04, 0x05}) {
		t.Errorf("atoms[1].data = %v, want [3 4 5]", atoms[1].data)
	}
}

func TestNewMP4Decrypter(t *testing.T) {
	keyMap := map[string][]byte{
		"kid1": {0x01, 0x02, 0x03},
		"kid2": {0x04, 0x05, 0x06},
	}

	d := NewMP4Decrypter(keyMap)

	if d == nil {
		t.Fatal("NewMP4Decrypter() returned nil")
	}
	if len(d.keyMap) != 2 {
		t.Errorf("keyMap length = %d, want 2", len(d.keyMap))
	}
}

func TestGetKeyForTrack(t *testing.T) {
	tests := []struct {
		name    string
		keyMap  map[string][]byte
		trackID int
		wantNil bool
	}{
		{
			name:    "empty keymap",
			keyMap:  map[string][]byte{},
			trackID: 1,
			wantNil: true,
		},
		{
			name: "single key",
			keyMap: map[string][]byte{
				"kid1": {0x01, 0x02},
			},
			trackID: 1,
			wantNil: false,
		},
		{
			name: "multi key track 1",
			keyMap: map[string][]byte{
				"kid1": {0x01},
				"kid2": {0x02},
			},
			trackID: 1,
			wantNil: false,
		},
		{
			name: "multi key track 2",
			keyMap: map[string][]byte{
				"kid1": {0x01},
				"kid2": {0x02},
			},
			trackID: 2,
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewMP4Decrypter(tt.keyMap)
			key := d.getKeyForTrack(tt.trackID)
			if tt.wantNil && key != nil {
				t.Errorf("getKeyForTrack() = %v, want nil", key)
			}
			if !tt.wantNil && key == nil {
				t.Errorf("getKeyForTrack() = nil, want non-nil")
			}
		})
	}
}

func TestProcessSample_Unencrypted(t *testing.T) {
	d := NewMP4Decrypter(map[string][]byte{
		"kid": bytes.Repeat([]byte{0x01}, 16),
	})
	d.currentKey = d.keyMap["kid"]

	sample := []byte{0x01, 0x02, 0x03, 0x04}
	info := sampleAuxInfo{isEncrypted: false}

	result, err := d.processSample(sample, info)
	if err != nil {
		t.Fatalf("processSample() error = %v", err)
	}

	if !bytes.Equal(result, sample) {
		t.Errorf("processSample() = %v, want %v", result, sample)
	}
}

func TestProcessSample_FullEncryption(t *testing.T) {
	// Use a known key for testing
	key := bytes.Repeat([]byte{0x00}, 16)
	d := NewMP4Decrypter(map[string][]byte{
		"kid": key,
	})
	d.currentKey = key

	// With all-zero key and all-zero IV, CTR mode XORs with a predictable keystream
	iv := make([]byte, 8)
	info := sampleAuxInfo{
		isEncrypted: true,
		iv:          iv,
		subSamples:  nil, // No subsamples = full encryption
	}

	// Test that decrypt(encrypt(data)) = data
	original := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}

	// First "encrypt" (XOR with keystream)
	encrypted, err := d.processSample(original, info)
	if err != nil {
		t.Fatalf("processSample() encrypt error = %v", err)
	}

	// Then "decrypt" (XOR again should give back original)
	decrypted, err := d.processSample(encrypted, info)
	if err != nil {
		t.Fatalf("processSample() decrypt error = %v", err)
	}

	if !bytes.Equal(decrypted, original) {
		t.Errorf("processSample() round-trip failed: got %v, want %v", decrypted, original)
	}
}

func TestProcessSample_SubsampleEncryption(t *testing.T) {
	key := bytes.Repeat([]byte{0x00}, 16)
	d := NewMP4Decrypter(map[string][]byte{
		"kid": key,
	})
	d.currentKey = key

	iv := make([]byte, 8)
	info := sampleAuxInfo{
		isEncrypted: true,
		iv:          iv,
		subSamples: []subSampleEntry{
			{clearBytes: 2, encryptedBytes: 4}, // First 2 bytes clear, next 4 encrypted
			{clearBytes: 1, encryptedBytes: 3}, // Next 1 byte clear, next 3 encrypted
		},
	}

	// Sample: [C C E E E E C E E E] where C=clear, E=encrypted
	sample := make([]byte, 10)
	for i := range sample {
		sample[i] = byte(i)
	}

	result, err := d.processSample(sample, info)
	if err != nil {
		t.Fatalf("processSample() error = %v", err)
	}

	// Clear bytes should be unchanged
	if result[0] != 0 || result[1] != 1 {
		t.Errorf("clear bytes modified: got %v, want [0 1]", result[:2])
	}
	if result[6] != 6 {
		t.Errorf("clear byte at index 6 modified: got %v, want 6", result[6])
	}

	// Result length should match input
	if len(result) != len(sample) {
		t.Errorf("result length = %d, want %d", len(result), len(sample))
	}
}

func TestDecryptSegmentWithKeys_InvalidKey(t *testing.T) {
	init := []byte{0x01, 0x02}
	segment := []byte{0x03, 0x04}

	_, err := DecryptSegmentWithKeys(init, segment, "kid1,kid2", "key1")
	if err == nil {
		t.Error("DecryptSegmentWithKeys() expected error for mismatched key count")
	}
}

func TestDecryptSegmentWithKeys_InvalidHex(t *testing.T) {
	init := []byte{0x01, 0x02}
	segment := []byte{0x03, 0x04}

	_, err := DecryptSegmentWithKeys(init, segment, "kid", "xyz")
	if err == nil {
		t.Error("DecryptSegmentWithKeys() expected error for invalid hex")
	}
}

func TestParseSenc(t *testing.T) {
	d := NewMP4Decrypter(nil)

	// Build a minimal senc box data
	// Version 0, flags with subsample info (0x02)
	// Sample count: 2
	// Sample 1: IV (8 bytes) + subsample count (2) + 2 subsamples
	// Sample 2: IV (8 bytes) only (no subsamples due to flag check per sample)

	var data bytes.Buffer
	// Version (1 byte) + Flags (3 bytes) = 0x00000002
	data.Write([]byte{0x00, 0x00, 0x00, 0x02})
	// Sample count
	data.Write([]byte{0x00, 0x00, 0x00, 0x01})
	// Sample 1 IV (8 bytes)
	data.Write([]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08})
	// Subsample count
	data.Write([]byte{0x00, 0x01})
	// Subsample: clear_bytes (2) + encrypted_bytes (4)
	data.Write([]byte{0x00, 0x10}) // clear = 16
	data.Write([]byte{0x00, 0x00, 0x00, 0x20}) // encrypted = 32

	atom := mp4Atom{
		atomType: "senc",
		data:     data.Bytes(),
	}

	info := d.parseSenc(atom, 1)

	if len(info) != 1 {
		t.Fatalf("parseSenc() got %d samples, want 1", len(info))
	}

	if !info[0].isEncrypted {
		t.Error("sample should be encrypted")
	}

	expectedIV := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	if !bytes.Equal(info[0].iv, expectedIV) {
		t.Errorf("IV = %v, want %v", info[0].iv, expectedIV)
	}

	if len(info[0].subSamples) != 1 {
		t.Fatalf("got %d subsamples, want 1", len(info[0].subSamples))
	}

	if info[0].subSamples[0].clearBytes != 16 {
		t.Errorf("clearBytes = %d, want 16", info[0].subSamples[0].clearBytes)
	}
	if info[0].subSamples[0].encryptedBytes != 32 {
		t.Errorf("encryptedBytes = %d, want 32", info[0].subSamples[0].encryptedBytes)
	}
}

func TestExtractCodecFormat(t *testing.T) {
	d := NewMP4Decrypter(nil)

	// Build sinf atom containing frma atom
	frmaData := []byte("avc1")
	frmaAtom := packAtom("frma", frmaData)

	sinf := mp4Atom{
		atomType: "sinf",
		data:     frmaAtom,
	}

	format := d.extractCodecFormat(sinf)
	if format != "avc1" {
		t.Errorf("extractCodecFormat() = %s, want 'avc1'", format)
	}
}

func TestExtractCodecFormat_NoFrma(t *testing.T) {
	d := NewMP4Decrypter(nil)

	// sinf without frma
	schm := packAtom("schm", []byte{0x01, 0x02})
	sinf := mp4Atom{
		atomType: "sinf",
		data:     schm,
	}

	format := d.extractCodecFormat(sinf)
	if format != "" {
		t.Errorf("extractCodecFormat() = %s, want empty string", format)
	}
}
