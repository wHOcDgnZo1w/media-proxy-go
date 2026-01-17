// Package crypto provides CENC decryption for fMP4 segments.
package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"fmt"
	"strings"
)

// MP4Decrypter decrypts CENC-encrypted MP4 segments.
type MP4Decrypter struct {
	keyMap           map[string][]byte // KID (hex) -> Key (bytes)
	currentKey       []byte
	trunSampleSizes  []uint32
	currentSampleInfo []sampleAuxInfo
	encryptionOverhead int
}

type sampleAuxInfo struct {
	isEncrypted bool
	iv          []byte
	subSamples  []subSampleEntry
}

type subSampleEntry struct {
	clearBytes     uint16
	encryptedBytes uint32
}

// NewMP4Decrypter creates a new decrypter with the given key map.
// keyMap format: map[KID_hex]KEY_bytes
func NewMP4Decrypter(keyMap map[string][]byte) *MP4Decrypter {
	return &MP4Decrypter{
		keyMap: keyMap,
	}
}

// DecryptSegment decrypts a combined init+media segment.
func (d *MP4Decrypter) DecryptSegment(combined []byte) ([]byte, error) {
	atoms := parseAtoms(combined)

	processOrder := []string{"moov", "moof", "sidx", "mdat"}
	processed := make(map[string][]byte)

	for _, atomType := range processOrder {
		for _, atom := range atoms {
			if atom.atomType == atomType {
				var err error
				processed[atomType], err = d.processAtom(atomType, atom)
				if err != nil {
					return nil, err
				}
				break
			}
		}
	}

	// Rebuild the output
	var result bytes.Buffer
	for _, atom := range atoms {
		if data, ok := processed[atom.atomType]; ok {
			result.Write(data)
		} else {
			result.Write(packAtom(atom.atomType, atom.data))
		}
	}

	return result.Bytes(), nil
}

type mp4Atom struct {
	atomType string
	size     int
	data     []byte
}

func parseAtoms(data []byte) []mp4Atom {
	var atoms []mp4Atom
	pos := 0

	for pos+8 <= len(data) {
		size := int(binary.BigEndian.Uint32(data[pos:]))
		atomType := string(data[pos+4 : pos+8])
		headerSize := 8

		if size == 1 && pos+16 <= len(data) {
			size = int(binary.BigEndian.Uint64(data[pos+8:]))
			headerSize = 16
		}

		if size < 8 || pos+size > len(data) {
			break
		}

		atoms = append(atoms, mp4Atom{
			atomType: atomType,
			size:     size,
			data:     data[pos+headerSize : pos+size],
		})
		pos += size
	}

	return atoms
}

func packAtom(atomType string, data []byte) []byte {
	size := len(data) + 8
	result := make([]byte, size)
	binary.BigEndian.PutUint32(result, uint32(size))
	copy(result[4:8], atomType)
	copy(result[8:], data)
	return result
}

func (d *MP4Decrypter) processAtom(atomType string, atom mp4Atom) ([]byte, error) {
	switch atomType {
	case "moov":
		return d.processMoov(atom)
	case "moof":
		return d.processMoof(atom)
	case "sidx":
		return d.processSidx(atom)
	case "mdat":
		return d.decryptMdat(atom)
	default:
		return packAtom(atomType, atom.data), nil
	}
}

func (d *MP4Decrypter) processMoov(moov mp4Atom) ([]byte, error) {
	atoms := parseAtoms(moov.data)
	var newData bytes.Buffer

	for _, atom := range atoms {
		if atom.atomType == "trak" {
			trakData, _ := d.processTrak(atom)
			newData.Write(trakData)
		} else if atom.atomType != "pssh" {
			// Skip PSSH boxes
			newData.Write(packAtom(atom.atomType, atom.data))
		}
	}

	return packAtom("moov", newData.Bytes()), nil
}

func (d *MP4Decrypter) processMoof(moof mp4Atom) ([]byte, error) {
	atoms := parseAtoms(moof.data)
	var newData bytes.Buffer

	for _, atom := range atoms {
		if atom.atomType == "traf" {
			trafData, _ := d.processTraf(atom)
			newData.Write(trafData)
		} else {
			newData.Write(packAtom(atom.atomType, atom.data))
		}
	}

	return packAtom("moof", newData.Bytes()), nil
}

func (d *MP4Decrypter) processTraf(traf mp4Atom) ([]byte, error) {
	atoms := parseAtoms(traf.data)
	var newData bytes.Buffer
	var tfhd mp4Atom
	var sampleCount int
	var sampleInfo []sampleAuxInfo

	// Calculate encryption overhead
	d.encryptionOverhead = 0
	for _, atom := range atoms {
		if atom.atomType == "senc" || atom.atomType == "saiz" || atom.atomType == "saio" {
			d.encryptionOverhead += atom.size
		}
	}

	for _, atom := range atoms {
		switch atom.atomType {
		case "tfhd":
			tfhd = atom
			newData.Write(packAtom(atom.atomType, atom.data))
		case "trun":
			sampleCount = d.processTrun(atom)
			modifiedTrun := d.modifyTrun(atom)
			newData.Write(modifiedTrun)
		case "senc":
			sampleInfo = d.parseSenc(atom, sampleCount)
		case "saiz", "saio":
			// Skip these encryption-related boxes
		default:
			newData.Write(packAtom(atom.atomType, atom.data))
		}
	}

	// Set current key based on track ID
	if len(tfhd.data) >= 8 {
		trackID := binary.BigEndian.Uint32(tfhd.data[4:8])
		d.currentKey = d.getKeyForTrack(int(trackID))
		d.currentSampleInfo = sampleInfo
	}

	return packAtom("traf", newData.Bytes()), nil
}

func (d *MP4Decrypter) processTrun(trun mp4Atom) int {
	if len(trun.data) < 8 {
		return 0
	}

	flags := binary.BigEndian.Uint32(trun.data[0:4]) & 0xFFFFFF
	sampleCount := int(binary.BigEndian.Uint32(trun.data[4:8]))

	offset := 8
	if flags&0x000001 != 0 {
		offset += 4 // data-offset-present
	}
	if flags&0x000004 != 0 {
		offset += 4 // first-sample-flags-present
	}

	d.trunSampleSizes = make([]uint32, sampleCount)

	for i := 0; i < sampleCount && offset < len(trun.data); i++ {
		if flags&0x000100 != 0 {
			offset += 4 // sample-duration-present
		}
		if flags&0x000200 != 0 && offset+4 <= len(trun.data) {
			d.trunSampleSizes[i] = binary.BigEndian.Uint32(trun.data[offset:])
			offset += 4
		}
		if flags&0x000400 != 0 {
			offset += 4 // sample-flags-present
		}
		if flags&0x000800 != 0 {
			offset += 4 // sample-composition-time-offsets-present
		}
	}

	return sampleCount
}

func (d *MP4Decrypter) modifyTrun(trun mp4Atom) []byte {
	data := make([]byte, len(trun.data))
	copy(data, trun.data)

	flags := binary.BigEndian.Uint32(data[0:4]) & 0xFFFFFF

	// If data-offset-present, update the offset
	if flags&0x000001 != 0 && len(data) >= 12 {
		currentOffset := int32(binary.BigEndian.Uint32(data[8:12]))
		newOffset := currentOffset - int32(d.encryptionOverhead)
		binary.BigEndian.PutUint32(data[8:12], uint32(newOffset))
	}

	return packAtom("trun", data)
}

func (d *MP4Decrypter) parseSenc(senc mp4Atom, sampleCount int) []sampleAuxInfo {
	if len(senc.data) < 4 {
		return nil
	}

	versionFlags := binary.BigEndian.Uint32(senc.data[0:4])
	flags := versionFlags & 0xFFFFFF
	pos := 4

	if versionFlags>>24 == 0 {
		if pos+4 > len(senc.data) {
			return nil
		}
		sampleCount = int(binary.BigEndian.Uint32(senc.data[pos:]))
		pos += 4
	}

	var info []sampleAuxInfo

	for i := 0; i < sampleCount && pos+8 <= len(senc.data); i++ {
		iv := make([]byte, 8)
		copy(iv, senc.data[pos:pos+8])
		pos += 8

		var subSamples []subSampleEntry

		if flags&0x000002 != 0 && pos+2 <= len(senc.data) {
			subSampleCount := int(binary.BigEndian.Uint16(senc.data[pos:]))
			pos += 2

			for j := 0; j < subSampleCount && pos+6 <= len(senc.data); j++ {
				clearBytes := binary.BigEndian.Uint16(senc.data[pos:])
				encryptedBytes := binary.BigEndian.Uint32(senc.data[pos+2:])
				pos += 6
				subSamples = append(subSamples, subSampleEntry{clearBytes, encryptedBytes})
			}
		}

		info = append(info, sampleAuxInfo{
			isEncrypted: true,
			iv:          iv,
			subSamples:  subSamples,
		})
	}

	return info
}

func (d *MP4Decrypter) getKeyForTrack(trackID int) []byte {
	if len(d.keyMap) == 0 {
		return nil
	}
	if len(d.keyMap) == 1 {
		for _, key := range d.keyMap {
			return key
		}
	}

	// Multi-key: return by index based on track ID
	keys := make([][]byte, 0, len(d.keyMap))
	for _, key := range d.keyMap {
		keys = append(keys, key)
	}
	keyIndex := (trackID - 1) % len(keys)
	return keys[keyIndex]
}

func (d *MP4Decrypter) decryptMdat(mdat mp4Atom) ([]byte, error) {
	if d.currentKey == nil || len(d.currentSampleInfo) == 0 {
		return packAtom("mdat", mdat.data), nil
	}

	var decrypted bytes.Buffer
	pos := 0

	for i, info := range d.currentSampleInfo {
		if pos >= len(mdat.data) {
			break
		}

		var sampleSize int
		if i < len(d.trunSampleSizes) {
			sampleSize = int(d.trunSampleSizes[i])
		} else {
			sampleSize = len(mdat.data) - pos
		}

		if pos+sampleSize > len(mdat.data) {
			sampleSize = len(mdat.data) - pos
		}

		sample := mdat.data[pos : pos+sampleSize]
		pos += sampleSize

		decryptedSample, err := d.processSample(sample, info)
		if err != nil {
			return nil, err
		}
		decrypted.Write(decryptedSample)
	}

	return packAtom("mdat", decrypted.Bytes()), nil
}

func (d *MP4Decrypter) processSample(sample []byte, info sampleAuxInfo) ([]byte, error) {
	if !info.isEncrypted || d.currentKey == nil {
		return sample, nil
	}

	// Pad IV to 16 bytes
	iv := make([]byte, 16)
	copy(iv, info.iv)

	block, err := aes.NewCipher(d.currentKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	stream := cipher.NewCTR(block, iv)

	if len(info.subSamples) == 0 {
		// Decrypt entire sample
		result := make([]byte, len(sample))
		stream.XORKeyStream(result, sample)
		return result, nil
	}

	// Handle subsample encryption
	var result bytes.Buffer
	offset := 0

	for _, sub := range info.subSamples {
		// Copy clear bytes
		clearEnd := offset + int(sub.clearBytes)
		if clearEnd > len(sample) {
			clearEnd = len(sample)
		}
		result.Write(sample[offset:clearEnd])
		offset = clearEnd

		// Decrypt encrypted bytes
		encEnd := offset + int(sub.encryptedBytes)
		if encEnd > len(sample) {
			encEnd = len(sample)
		}
		encrypted := sample[offset:encEnd]
		decrypted := make([]byte, len(encrypted))
		stream.XORKeyStream(decrypted, encrypted)
		result.Write(decrypted)
		offset = encEnd
	}

	// Handle remaining data as encrypted
	if offset < len(sample) {
		remaining := sample[offset:]
		decrypted := make([]byte, len(remaining))
		stream.XORKeyStream(decrypted, remaining)
		result.Write(decrypted)
	}

	return result.Bytes(), nil
}

func (d *MP4Decrypter) processTrak(trak mp4Atom) ([]byte, error) {
	atoms := parseAtoms(trak.data)
	var newData bytes.Buffer

	for _, atom := range atoms {
		if atom.atomType == "mdia" {
			mdiaData, _ := d.processMdia(atom)
			newData.Write(mdiaData)
		} else {
			newData.Write(packAtom(atom.atomType, atom.data))
		}
	}

	return packAtom("trak", newData.Bytes()), nil
}

func (d *MP4Decrypter) processMdia(mdia mp4Atom) ([]byte, error) {
	atoms := parseAtoms(mdia.data)
	var newData bytes.Buffer

	for _, atom := range atoms {
		if atom.atomType == "minf" {
			minfData, _ := d.processMinf(atom)
			newData.Write(minfData)
		} else {
			newData.Write(packAtom(atom.atomType, atom.data))
		}
	}

	return packAtom("mdia", newData.Bytes()), nil
}

func (d *MP4Decrypter) processMinf(minf mp4Atom) ([]byte, error) {
	atoms := parseAtoms(minf.data)
	var newData bytes.Buffer

	for _, atom := range atoms {
		if atom.atomType == "stbl" {
			stblData, _ := d.processStbl(atom)
			newData.Write(stblData)
		} else {
			newData.Write(packAtom(atom.atomType, atom.data))
		}
	}

	return packAtom("minf", newData.Bytes()), nil
}

func (d *MP4Decrypter) processStbl(stbl mp4Atom) ([]byte, error) {
	atoms := parseAtoms(stbl.data)
	var newData bytes.Buffer

	for _, atom := range atoms {
		if atom.atomType == "stsd" {
			stsdData, _ := d.processStsd(atom)
			newData.Write(stsdData)
		} else {
			newData.Write(packAtom(atom.atomType, atom.data))
		}
	}

	return packAtom("stbl", newData.Bytes()), nil
}

func (d *MP4Decrypter) processStsd(stsd mp4Atom) ([]byte, error) {
	if len(stsd.data) < 8 {
		return packAtom("stsd", stsd.data), nil
	}

	entryCount := int(binary.BigEndian.Uint32(stsd.data[4:8]))
	var newData bytes.Buffer
	newData.Write(stsd.data[:8]) // version_flags + entry_count

	atoms := parseAtoms(stsd.data[8:])
	for i := 0; i < entryCount && i < len(atoms); i++ {
		processedEntry := d.processSampleEntry(atoms[i])
		newData.Write(processedEntry)
	}

	return packAtom("stsd", newData.Bytes()), nil
}

func (d *MP4Decrypter) processSampleEntry(entry mp4Atom) []byte {
	// Determine fixed field size based on entry type
	var fixedSize int
	switch entry.atomType {
	case "mp4a", "enca":
		fixedSize = 28
	case "mp4v", "encv", "avc1", "hev1", "hvc1":
		fixedSize = 78
	default:
		fixedSize = 16
	}

	if fixedSize > len(entry.data) {
		fixedSize = len(entry.data)
	}

	var newData bytes.Buffer
	newData.Write(entry.data[:fixedSize])

	var codecFormat string
	childAtoms := parseAtoms(entry.data[fixedSize:])

	for _, atom := range childAtoms {
		switch atom.atomType {
		case "sinf":
			codecFormat = d.extractCodecFormat(atom)
		case "schi", "tenc", "schm":
			// Skip encryption-related atoms
		default:
			newData.Write(packAtom(atom.atomType, atom.data))
		}
	}

	// Use extracted codec format or original type
	newType := entry.atomType
	if codecFormat != "" {
		newType = codecFormat
	}

	return packAtom(newType, newData.Bytes())
}

func (d *MP4Decrypter) extractCodecFormat(sinf mp4Atom) string {
	atoms := parseAtoms(sinf.data)
	for _, atom := range atoms {
		if atom.atomType == "frma" && len(atom.data) >= 4 {
			return string(atom.data[:4])
		}
	}
	return ""
}

func (d *MP4Decrypter) processSidx(sidx mp4Atom) ([]byte, error) {
	if len(sidx.data) < 36 {
		return packAtom("sidx", sidx.data), nil
	}

	data := make([]byte, len(sidx.data))
	copy(data, sidx.data)

	currentSize := binary.BigEndian.Uint32(data[32:36])
	referenceType := currentSize >> 31
	referencedSize := currentSize & 0x7FFFFFFF

	newReferencedSize := referencedSize - uint32(d.encryptionOverhead)
	newSize := (referenceType << 31) | newReferencedSize
	binary.BigEndian.PutUint32(data[32:36], newSize)

	return packAtom("sidx", data), nil
}

// DecryptSegmentWithKeys is a convenience function to decrypt a segment.
// keyID and key can be comma-separated for multi-key support.
func DecryptSegmentWithKeys(initSegment, mediaSegment []byte, keyID, key string) ([]byte, error) {
	keyMap := make(map[string][]byte)

	kids := strings.Split(keyID, ",")
	keys := strings.Split(key, ",")

	if len(kids) != len(keys) {
		return nil, fmt.Errorf("mismatched key_id/key count: %d vs %d", len(kids), len(keys))
	}

	for i := range kids {
		kid := strings.TrimSpace(kids[i])
		k := strings.TrimSpace(keys[i])

		keyBytes, err := hexToBytes(k)
		if err != nil {
			return nil, fmt.Errorf("invalid key hex: %w", err)
		}
		keyMap[kid] = keyBytes
	}

	combined := append(initSegment, mediaSegment...)
	decrypter := NewMP4Decrypter(keyMap)
	return decrypter.DecryptSegment(combined)
}

func hexToBytes(hex string) ([]byte, error) {
	if len(hex)%2 != 0 {
		return nil, fmt.Errorf("odd length hex string")
	}

	result := make([]byte, len(hex)/2)
	for i := 0; i < len(hex); i += 2 {
		var b byte
		_, err := fmt.Sscanf(hex[i:i+2], "%02x", &b)
		if err != nil {
			return nil, err
		}
		result[i/2] = b
	}
	return result, nil
}
