package main

import "testing"

func TestMergeAndFastestPick(t *testing.T) {
	idx := 0
	a := &addonStream{Name: "Torrentio\n1080p", Title: "Film.1080p.x264\n👤 40 💾 8 GB", InfoHash: "AAAA", FileIdx: &idx, Addon: "Torrentio"}
	b := &addonStream{Name: "KC\n1080p", Title: "Film.1080p.x264\n👤 55 💾 8 GB", InfoHash: "aaaa", FileIdx: &idx, Addon: "Knightcrawler"}
	c := &addonStream{Name: "Torrentio\n720p", Title: "Film.720p.x264\n👤 30 💾 1.1 GB", InfoHash: "BBBB", Addon: "Torrentio"}
	h := &addonStream{Name: "Torrentio\n2160p", Title: "Film.2160p.HEVC\n👤 900 💾 30 GB", InfoHash: "CCCC", Addon: "Torrentio"}
	var list []*addonStream
	for _, s := range []*addonStream{a, b, c, h} {
		list = mergeStream(list, s)
	}
	if len(list) != 3 || list[0].seeders() != 55 || list[0].Addon != "Torrentio + Knightcrawler" {
		t.Fatalf("merge: %d streams, first %d seeders from %q", len(list), list[0].seeders(), list[0].Addon)
	}
	if p := quickPick(list); p != c {
		t.Fatalf("starts fastest should be the small 720p, got %q", p.Title)
	}
	if alts := alternatives(list, c); len(alts) != 1 || alts[0] != a {
		t.Fatalf("alternatives: %d", len(alts))
	}
	if got := parseTrackers("udp://a:1/announce\n\nhttps://b/announce\nfoo"); len(got) != 2 {
		t.Fatalf("trackers: %v", got)
	}
}
