package taskstore

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const ownerRejoinPayloadMagic = "TCORJP\x00\x01"
const ownerRejoinPayloadHeader = 12
const maxOwnerRejoinPayloadBytes = 160 << 20

// OwnerRejoinFileBytes carries the exact before/after content.  Nil is only
// valid where the corresponding plan presence bit is false.
type OwnerRejoinFileBytes struct {
	Role     string
	Original []byte
	Target   []byte
}

// OwnerRejoinPayloadBytes frames files in plan role order. It neither writes
// an artifact nor accepts an unbound target; callers persist its digest in the
// plan before serializing that plan.
func OwnerRejoinPayloadBytes(p OwnerRejoinPlan, files []OwnerRejoinFileBytes) ([]byte, error) {
	if err := validateOwnerRejoinPlan(p, false); err != nil {
		return nil, err
	}
	if p.SourceCommonAvailable {
		if err := validateSameCommonOwnerRejoinPlan(p, files); err != nil {
			return nil, err
		}
	}
	if len(files) != len(p.Files) {
		return nil, errors.New("owner rejoin payload file count mismatch")
	}
	raw := make([]byte, ownerRejoinPayloadHeader)
	copy(raw, ownerRejoinPayloadMagic)
	binary.BigEndian.PutUint32(raw[8:12], uint32(len(files)))
	for i, f := range files {
		meta := p.Files[i]
		if f.Role != string(meta.Role) || len(f.Role) == 0 || len(f.Role) > 255 || (!meta.OriginalPresent && f.Original != nil) || (!meta.TargetPresent && f.Target != nil) || len(f.Original) != meta.OriginalLength || len(f.Target) != meta.TargetLength || (meta.OriginalPresent && bytesDigest(f.Original) != meta.OriginalSHA256) || (meta.TargetPresent && bytesDigest(f.Target) != meta.TargetSHA256) {
			return nil, errors.New("owner rejoin payload differs from plan")
		}
		if len(raw) > maxOwnerRejoinPayloadBytes-len(f.Role)-25 || len(f.Original) > maxOwnerRejoinPayloadBytes-len(raw)-len(f.Role)-25 || len(f.Target) > maxOwnerRejoinPayloadBytes-len(raw)-len(f.Role)-25-len(f.Original) {
			return nil, errors.New("owner rejoin payload exceeds bound")
		}
		raw = append(raw, byte(len(f.Role)))
		raw = append(raw, f.Role...)
		flags := byte(0)
		if meta.OriginalPresent {
			flags |= 1
		}
		if meta.TargetPresent {
			flags |= 2
		}
		raw = append(raw, flags)
		var fixed [20]byte
		binary.BigEndian.PutUint32(fixed[:4], meta.Mode)
		binary.BigEndian.PutUint64(fixed[4:12], uint64(len(f.Original)))
		binary.BigEndian.PutUint64(fixed[12:20], uint64(len(f.Target)))
		raw = append(raw, fixed[:]...)
		raw = append(raw, f.Original...)
		raw = append(raw, f.Target...)
	}
	return raw, nil
}

func DecodeOwnerRejoinPayload(raw []byte, p OwnerRejoinPlan) ([]OwnerRejoinFileBytes, error) {
	if err := validateOwnerRejoinPlan(p, true); err != nil {
		return nil, err
	}
	if len(raw) < ownerRejoinPayloadHeader || len(raw) > maxOwnerRejoinPayloadBytes || string(raw[:8]) != ownerRejoinPayloadMagic || bytesDigest(raw) != p.PayloadSHA256 {
		return nil, errors.New("owner rejoin payload header or digest invalid")
	}
	count := binary.BigEndian.Uint32(raw[8:12])
	if count != uint32(len(p.Files)) {
		return nil, errors.New("owner rejoin payload count mismatch")
	}
	off := ownerRejoinPayloadHeader
	out := make([]OwnerRejoinFileBytes, 0, count)
	for i, meta := range p.Files {
		if off >= len(raw) {
			return nil, errors.New("owner rejoin payload truncated role")
		}
		n := int(raw[off])
		off++
		if n == 0 || off+n+21 > len(raw) {
			return nil, errors.New("owner rejoin payload role framing invalid")
		}
		role := string(raw[off : off+n])
		off += n
		flags := raw[off]
		off++
		if flags&^3 != 0 || (flags&1 != 0) != meta.OriginalPresent || (flags&2 != 0) != meta.TargetPresent || role != string(meta.Role) {
			return nil, errors.New("owner rejoin payload role or presence mismatch")
		}
		mode := binary.BigEndian.Uint32(raw[off : off+4])
		originalLength, targetLength := binary.BigEndian.Uint64(raw[off+4:off+12]), binary.BigEndian.Uint64(raw[off+12:off+20])
		off += 20
		if mode != meta.Mode || originalLength > uint64(maxArchiveCapacityBytes) || targetLength > uint64(maxArchiveCapacityBytes) || originalLength != uint64(meta.OriginalLength) || targetLength != uint64(meta.TargetLength) || originalLength > uint64(len(raw)-off) || targetLength > uint64(len(raw)-off)-originalLength {
			return nil, errors.New("owner rejoin payload lengths invalid")
		}
		o := int(originalLength)
		t := int(targetLength)
		f := OwnerRejoinFileBytes{Role: role, Original: append([]byte(nil), raw[off:off+o]...), Target: append([]byte(nil), raw[off+o:off+o+t]...)}
		off += o + t
		if (!meta.OriginalPresent && f.Original != nil) || (!meta.TargetPresent && f.Target != nil) || (meta.OriginalPresent && bytesDigest(f.Original) != meta.OriginalSHA256) || (meta.TargetPresent && bytesDigest(f.Target) != meta.TargetSHA256) {
			return nil, fmt.Errorf("owner rejoin payload file %d digest mismatch", i)
		}
		out = append(out, f)
	}
	if off != len(raw) {
		return nil, errors.New("owner rejoin payload trailing bytes")
	}
	if p.SourceCommonAvailable {
		if err := validateSameCommonOwnerRejoinPlan(p, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// BindOwnerRejoinPayload returns a copy of plan with the deterministic payload
// digest. It is deliberately pure so d2 can choose its own durable sequencing.
func BindOwnerRejoinPayload(p OwnerRejoinPlan, files []OwnerRejoinFileBytes) (OwnerRejoinPlan, []byte, error) {
	p.PayloadSHA256 = ""
	raw, err := OwnerRejoinPayloadBytes(p, files)
	if err != nil {
		return OwnerRejoinPlan{}, nil, err
	}
	p.PayloadSHA256 = bytesDigest(raw)
	if err := validateOwnerRejoinPlan(p, true); err != nil {
		return OwnerRejoinPlan{}, nil, err
	}
	return p, raw, nil
}
