package taskstore

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

const archiveCapacityFile = ".task-manager-archive-capacity.json"
const archiveCapacityPayloadMagic = "TCARCP\x00\x01"
const archiveCapacityPayloadHeader = len(archiveCapacityPayloadMagic) + 8 + 8 + 4
const maxArchiveCapacityPayloadBytes = archiveCapacityPayloadHeader + maxRepairsBytes + maxArchiveCapacityBytes

type archiveCapacityPending struct {
	Owner     string `json:"owner"`
	UpgradeID string `json:"upgradeId"`
}

type archiveCapacityAdoption struct {
	SchemaVersion       int    `json:"schemaVersion"`
	Phase               string `json:"phase"`
	UpgradeID           string `json:"upgradeId"`
	BoardPath           string `json:"boardPath"`
	Namespace           string `json:"namespace"`
	SourceJournalSchema int    `json:"sourceJournalSchema"`
	TargetJournalSchema int    `json:"targetJournalSchema"`
	StorageProtocol     int    `json:"storageProtocol"`
	JournalMode         uint32 `json:"journalMode"`
	OriginalLength      int    `json:"originalLength"`
	OriginalSHA256      string `json:"originalSha256"`
	TargetLength        int    `json:"targetLength"`
	TargetSHA256        string `json:"targetSha256"`
	PayloadSHA256       string `json:"payloadSha256"`
}

func validateArchiveCapacityPendingShape(raw []byte) error {
	return relocationShape(raw, []string{"owner", "upgradeId"}, nil)
}

func archiveCapacityPayloadName(digest string) (string, error) {
	if !sharedHex64.MatchString(digest) {
		return "", errors.New("invalid archive capacity payload digest")
	}
	return ".task-manager-archive-capacity-" + digest + ".bin", nil
}

func archiveCapacityPayloadBytes(original, target []byte, mode uint32) ([]byte, error) {
	if mode == 0 || mode&^0777 != 0 {
		return nil, errors.New("invalid archive capacity journal mode")
	}
	if _, err := decodeArchiveJournal(original); err != nil {
		return nil, fmt.Errorf("capacity original: %w", err)
	}
	decoded, err := decodeArchiveCapacityJournal(target)
	if err != nil {
		return nil, fmt.Errorf("capacity target: %w", err)
	}
	if decoded.SchemaVersion != 2 {
		return nil, errors.New("capacity target schema must be 2")
	}
	originalJournal, _ := decodeArchiveJournal(original)
	want := originalJournal
	want.SchemaVersion = 2
	wantRaw, err := archiveCapacityJournalBytes(want)
	if err != nil || !bytes.Equal(wantRaw, target) {
		return nil, errors.New("capacity target differs from exact schema conversion")
	}
	raw := make([]byte, archiveCapacityPayloadHeader, archiveCapacityPayloadHeader+len(original)+len(target))
	copy(raw, archiveCapacityPayloadMagic)
	binary.BigEndian.PutUint64(raw[8:16], uint64(len(original)))
	binary.BigEndian.PutUint64(raw[16:24], uint64(len(target)))
	binary.BigEndian.PutUint32(raw[24:28], mode)
	return append(append(raw, original...), target...), nil
}

func decodeArchiveCapacityPayload(raw []byte, digest string) (original, target []byte, mode uint32, err error) {
	if len(raw) < archiveCapacityPayloadHeader || len(raw) > maxArchiveCapacityPayloadBytes || !sharedHex64.MatchString(digest) || bytesDigest(raw) != digest {
		return nil, nil, 0, errors.New("archive capacity payload size or digest invalid")
	}
	if string(raw[:8]) != archiveCapacityPayloadMagic {
		return nil, nil, 0, errors.New("unsupported archive capacity payload version")
	}
	o, t := binary.BigEndian.Uint64(raw[8:16]), binary.BigEndian.Uint64(raw[16:24])
	if o == 0 || o > maxRepairsBytes || t == 0 || t > maxArchiveCapacityBytes || o+t != uint64(len(raw)-archiveCapacityPayloadHeader) {
		return nil, nil, 0, errors.New("archive capacity payload framing invalid")
	}
	mode = binary.BigEndian.Uint32(raw[24:28])
	original, target = append([]byte(nil), raw[archiveCapacityPayloadHeader:archiveCapacityPayloadHeader+int(o)]...), append([]byte(nil), raw[archiveCapacityPayloadHeader+int(o):]...)
	_, err = archiveCapacityPayloadBytes(original, target, mode)
	return
}

func validateArchiveCapacityAdoption(a archiveCapacityAdoption) error {
	if a.SchemaVersion != 1 || (a.Phase != "pending" && a.Phase != "completed") || !sharedHex32.MatchString(a.UpgradeID) || !validSharedRoot(a.BoardPath) || (a.Namespace != "" && !sharedHex32.MatchString(a.Namespace)) || a.SourceJournalSchema != 1 || a.TargetJournalSchema != 2 || a.StorageProtocol != 5 || a.JournalMode == 0 || a.JournalMode&^0777 != 0 || a.OriginalLength <= 0 || a.OriginalLength > maxRepairsBytes || a.TargetLength <= 0 || a.TargetLength > maxArchiveCapacityBytes || !sharedHex64.MatchString(a.OriginalSHA256) || !sharedHex64.MatchString(a.TargetSHA256) || !sharedHex64.MatchString(a.PayloadSHA256) {
		return errors.New("invalid archive capacity adoption journal")
	}
	return nil
}

func archiveCapacityAdoptionBytes(a archiveCapacityAdoption) ([]byte, error) {
	if err := validateArchiveCapacityAdoption(a); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')
	if len(raw) > 4096 {
		return nil, errors.New("archive capacity adoption journal exceeds bound")
	}
	return raw, nil
}

func loadArchiveCapacityAdoption(r *os.Root) (archiveCapacityAdoption, error) {
	raw, err := boundedSnapshotFile(r, archiveCapacityFile, 4096)
	if err != nil {
		return archiveCapacityAdoption{}, err
	}
	if err := rejectDuplicateJSON(raw); err != nil {
		return archiveCapacityAdoption{}, err
	}
	if err := relocationShape(raw, []string{"schemaVersion", "phase", "upgradeId", "boardPath", "namespace", "sourceJournalSchema", "targetJournalSchema", "storageProtocol", "journalMode", "originalLength", "originalSha256", "targetLength", "targetSha256", "payloadSha256"}, nil); err != nil {
		return archiveCapacityAdoption{}, err
	}
	var a archiveCapacityAdoption
	if err := json.Unmarshal(raw, &a); err != nil {
		return a, err
	}
	return a, validateArchiveCapacityAdoption(a)
}

func saveArchiveCapacityAdoption(r *os.Root, a archiveCapacityAdoption, initial bool) error {
	raw, err := archiveCapacityAdoptionBytes(a)
	if err != nil {
		return err
	}
	name, err := stage(r, raw)
	if err != nil {
		return err
	}
	if initial {
		if err := r.Link(name, archiveCapacityFile); err != nil {
			return errors.Join(err, r.Remove(name))
		}
		return errors.Join(r.Remove(name), syncRoot(r))
	}
	if err := r.Rename(name, archiveCapacityFile); err != nil {
		return errors.Join(err, r.Remove(name))
	}
	return syncRoot(r)
}

func publishArchiveCapacityPayload(r *os.Root, original, target []byte, mode uint32) (string, error) {
	raw, err := archiveCapacityPayloadBytes(original, target, mode)
	if err != nil {
		return "", err
	}
	digest := bytesDigest(raw)
	name, err := archiveCapacityPayloadName(digest)
	if err != nil {
		return "", err
	}
	if info, err := r.Lstat(name); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
			return "", errors.New("archive capacity payload type or mode invalid")
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	} else {
		stageName, err := stage(r, raw)
		if err != nil {
			return "", err
		}
		if err := r.Link(stageName, name); err != nil {
			return "", errors.Join(err, r.Remove(stageName))
		}
		if err := r.Remove(stageName); err != nil {
			return "", err
		}
		if err := syncRoot(r); err != nil {
			return "", err
		}
	}
	_, _, _, err = loadArchiveCapacityPayload(r, digest)
	return digest, err
}

func loadArchiveCapacityPayload(r *os.Root, digest string) ([]byte, []byte, uint32, error) {
	name, err := archiveCapacityPayloadName(digest)
	if err != nil {
		return nil, nil, 0, err
	}
	info, err := r.Lstat(name)
	if err != nil {
		return nil, nil, 0, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() < int64(archiveCapacityPayloadHeader) || info.Size() > int64(maxArchiveCapacityPayloadBytes) {
		return nil, nil, 0, errors.New("archive capacity payload type, mode or size invalid")
	}
	f, err := r.Open(name)
	if err != nil {
		return nil, nil, 0, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(f, int64(maxArchiveCapacityPayloadBytes)+1))
	closeErr := f.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, nil, 0, err
	}
	return decodeArchiveCapacityPayload(raw, digest)
}
