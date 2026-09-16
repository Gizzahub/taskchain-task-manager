package githistory

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
)

func (s scanner) readBlobs(ctx context.Context, oids []string) ([]string, error) {
	if len(oids) == 0 {
		return []string{}, nil
	}
	raw, err := s.run(ctx, strings.Join(oids, "\n")+"\n", "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	return parseBlobs(raw, oids)
}

func parseBlobs(raw []byte, oids []string) ([]string, error) {
	r := bufio.NewReader(bytes.NewReader(raw))
	set := map[string]bool{}
	var total int64
	for _, oid := range oids {
		header, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		fields := strings.Fields(header)
		if len(fields) != 3 || fields[0] != oid || fields[1] != "blob" {
			return nil, errors.New("unexpected Git blob response")
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || size < 0 || size > 16<<20 || total > maxOutput-size {
			return nil, errors.New("invalid or oversized Git blob")
		}
		total += size
		body := make([]byte, int(size)+1)
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, err
		}
		if body[len(body)-1] != '\n' {
			return nil, errors.New("invalid Git blob delimiter")
		}
		ids, err := CandidateIDs(body[:size])
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			set[id] = true
		}
		if len(set) > 65536 {
			return nil, errors.New("history exceeds 65536 IDs")
		}
	}
	if _, err := r.ReadByte(); err != io.EOF {
		return nil, errors.New("trailing Git blob response")
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}
