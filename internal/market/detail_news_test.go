package market

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewsRSSAndJSONSelectSafeRecentArticles(t *testing.T) {
	now := fixtureTime(t, "2026-11-06T15:00:00Z")
	rss := []byte(`<?xml version="1.0"?><rss xmlns:n="urn:news"><channel>
		<item><title>Fed rate outlook &amp; tariffs</title><link>http://www.bing.com/news/apiclick.aspx?url=https%3A%2F%2Fexample.org%2Farticle</link><pubDate>Fri, 06 Nov 2026 14:23:00 GMT</pubDate><n:Source>Newswire</n:Source></item>
		<item><title>Same story</title><link>https://example.org/article?utm_source=feed</link><pubDate>Fri, 06 Nov 2026 14:00:00 GMT</pubDate><source>Newswire</source></item>
		<item><title>Insecure article</title><link>http://example.org/bad</link><pubDate>Fri, 06 Nov 2026 14:26:00 GMT</pubDate><source>Newswire</source></item>
		<item><title> </title><link>https://example.org/blank</link><pubDate>Fri, 06 Nov 2026 14:25:00 GMT</pubDate><source>Newswire</source></item>
		<item><title>Stale article</title><link>https://example.org/archive</link><pubDate>Fri, 16 Oct 2026 14:25:00 GMT</pubDate><source>Newswire</source></item>
	</channel></rss>`)
	items, ok := parseRSSNews(rss, now, false)
	if !ok || len(items) != 1 || items[0].URL != "https://example.org/article" || items[0].Category != "时政" ||
		items[0].Title != "Fed rate outlook & tariffs" || items[0].Source != "Newswire" {
		t.Fatalf("RSS feed items: %+v (%v)", items, ok)
	}
	jsonBody := fmt.Appendf(nil, `{"news":[
		{"title":"Nvidia earnings guidance","publisher":"Newswire","link":"https://example.org/earnings","providerPublishTime":%d,"relatedTickers":["NVDA"]},
		{"title":"Other company earnings","publisher":"Newswire","link":"https://example.org/other","providerPublishTime":%d,"relatedTickers":["AAPL"]},
		{"title":"Unrelated headline","publisher":"Newswire","link":"https://example.org/misc","providerPublishTime":%d,"relatedTickers":["NVDA"]}
	]}`, now.Unix(), now.Unix(), now.Unix())
	items, ok = parseYahooNews(jsonBody, "NVDA", "Nvidia", now)
	if !ok || len(items) != 1 || items[0].Category != "财报" {
		t.Fatalf("Yahoo company news: %+v (%v)", items, ok)
	}
}

func TestNewsHTTPSValidation(t *testing.T) {
	for _, raw := range []string{
		"http://example.org/news", "file:///C:/Windows/example", "javascript:alert(1)", "//example.org/news", "https:/example.org/news",
		"https://publisher.example@other.example/news", "https://publisher.example:444/news", "https://127.0.0.1/news",
		"https://publisher.local/news", "https://publisher.example/%0Anews", "https://publisher.example/%5Cnews",
		"https://publisher.example/\"news",
	} {
		if url := SecureNewsURL(raw); url != "" {
			t.Errorf("unsafe link accepted: %s", url)
		}
	}
	if url := SecureNewsURL("https://publisher.example:443/valid?a=%20"); url == "" {
		t.Fatal("valid publisher HTTPS URL blocked")
	}
	if raw := rssArticleURL("http://www.bing.com/news/apiclick.aspx?url=http%3A%2F%2Fexample.org%2Fnews"); raw != "" {
		t.Fatal("insecure redirect destination accepted")
	}
}

func TestDetailCacheCoalescesAndReleasesExpiredData(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var c detailCache[string, string]
	var calls atomic.Int32
	allow, completed := make(chan struct{}), make(chan struct{}, 1)
	load := func(ctx context.Context) (string, bool) {
		calls.Add(1)
		select {
		case <-allow:
			return "verified payload", true
		case <-ctx.Done():
			return "", false
		}
	}
	notify := func() { completed <- struct{}{} }
	ttl := 80 * time.Millisecond
	get := func(start bool) DetailResult[string] {
		return c.get(ctx, "symbol", start, ttl, DetailResult[string]{}, load, notify)
	}
	if get(false).Loading || c.entries != nil || calls.Load() != 0 {
		t.Fatal("a read without an open-trigger allocated or fetched")
	}
	for range 12 {
		if !get(true).Loading {
			t.Fatal("duplicate chart request was not in-flight")
		}
	}
	close(allow)
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("loader failed to notify")
	}
	if got := get(true); got.Value != "verified payload" || !got.OK || calls.Load() != 1 {
		t.Fatalf("expected a single cached result, got %+v (%d fetches)", got, calls.Load())
	}
	deadline := time.Now().Add(time.Second)
	for {
		c.mu.Lock()
		empty := c.entries == nil
		c.mu.Unlock()
		if empty {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expired news remained resident after the cache deadline")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
