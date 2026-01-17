package streams

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"media-proxy-go/pkg/httpclient"
	"media-proxy-go/pkg/interfaces"
	"media-proxy-go/pkg/logging"
	"media-proxy-go/pkg/types"
	"media-proxy-go/pkg/urlutil"
)

// MPDHandler processes DASH/MPD streams by converting to HLS on-the-fly.
type MPDHandler struct {
	client  *httpclient.Client
	log     *logging.Logger
	baseURL string
}

// NewMPDHandler creates a new MPD stream handler.
func NewMPDHandler(client *httpclient.Client, log *logging.Logger, baseURL string, _ interfaces.Transcoder) *MPDHandler {
	return &MPDHandler{
		client:  client,
		log:     log.WithComponent("mpd-handler"),
		baseURL: baseURL,
	}
}

// Type returns the stream type.
func (h *MPDHandler) Type() types.StreamType {
	return types.StreamTypeMPD
}

// CanHandle returns true if the URL appears to be a DASH stream.
func (h *MPDHandler) CanHandle(urlStr string) bool {
	lower := strings.ToLower(urlStr)
	return strings.Contains(lower, ".mpd") ||
		strings.Contains(lower, "/dash/") ||
		strings.Contains(lower, "manifest(format=mpd")
}

// HandleManifest handles MPD manifests by converting to HLS.
func (h *MPDHandler) HandleManifest(ctx context.Context, req *types.StreamRequest, baseURL string) (*types.StreamResponse, error) {
	h.log.Debug("handling MPD manifest", "url", req.URL)

	// Fetch the original MPD manifest
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	for key, value := range req.Headers {
		httpReq.Header.Set(key, value)
	}
	if httpReq.Header.Get("User-Agent") == "" {
		httpReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	}

	resp, err := h.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch MPD: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &types.StreamResponse{StatusCode: resp.StatusCode}, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read MPD: %w", err)
	}

	// Check if requesting specific representation (media playlist)
	if req.RepID != "" {
		playlist, err := h.convertMediaPlaylist(body, req.RepID, baseURL, req.URL, req.Headers, req.ClearKey)
		if err != nil {
			return nil, err
		}
		return &types.StreamResponse{
			ContentType: "application/vnd.apple.mpegurl",
			Body:        io.NopCloser(bytes.NewReader([]byte(playlist))),
			StatusCode:  http.StatusOK,
			Headers: map[string]string{
				"Cache-Control": "no-cache, no-store, must-revalidate",
			},
		}, nil
	}

	// Generate master playlist
	playlist, err := h.convertMasterPlaylist(body, baseURL, req.URL, req.Headers, req.ClearKey)
	if err != nil {
		return nil, err
	}

	return &types.StreamResponse{
		ContentType: "application/vnd.apple.mpegurl",
		Body:        io.NopCloser(bytes.NewReader([]byte(playlist))),
		StatusCode:  http.StatusOK,
		Headers: map[string]string{
			"Cache-Control": "no-cache, no-store, must-revalidate",
		},
	}, nil
}

// HandleSegment proxies an MPD segment.
func (h *MPDHandler) HandleSegment(ctx context.Context, req *types.StreamRequest) (*types.StreamResponse, error) {
	h.log.Debug("handling MPD segment", "url", req.URL)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	for key, value := range req.Headers {
		httpReq.Header.Set(key, value)
	}

	resp, err := h.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch segment: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		if strings.HasSuffix(req.URL, ".m4s") {
			contentType = "video/iso.segment"
		} else {
			contentType = "application/octet-stream"
		}
	}

	return &types.StreamResponse{
		ContentType: contentType,
		Body:        resp.Body,
		StatusCode:  resp.StatusCode,
	}, nil
}

// convertMasterPlaylist generates an HLS master playlist from MPD.
func (h *MPDHandler) convertMasterPlaylist(manifest []byte, proxyBaseURL, originalURL string, headers map[string]string, clearKey string) (string, error) {
	mpd, err := h.parseMPD(manifest)
	if err != nil {
		return "", err
	}

	var lines []string
	lines = append(lines, "#EXTM3U", "#EXT-X-VERSION:3")

	audioGroupID := "audio"
	hasAudio := false

	// Process audio tracks
	for _, period := range mpd.Periods {
		for _, as := range period.AdaptationSets {
			if !h.isAudio(as) {
				continue
			}
			for _, rep := range as.Representations {
				mediaURL := h.buildMediaPlaylistURL(proxyBaseURL, originalURL, rep.ID, headers, clearKey)
				lang := as.Lang
				if lang == "" {
					lang = "und"
				}
				name := fmt.Sprintf("Audio %s (%s)", lang, rep.Bandwidth)

				defaultAttr := "NO"
				if !hasAudio {
					defaultAttr = "YES"
				}

				lines = append(lines, fmt.Sprintf(
					`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="%s",NAME="%s",LANGUAGE="%s",DEFAULT=%s,AUTOSELECT=YES,URI="%s"`,
					audioGroupID, name, lang, defaultAttr, mediaURL,
				))
				hasAudio = true
			}
		}
	}

	// Find max video height for quality filtering
	maxHeight := 0
	for _, period := range mpd.Periods {
		for _, as := range period.AdaptationSets {
			if !h.isVideo(as) {
				continue
			}
			for _, rep := range as.Representations {
				if rep.Height > maxHeight {
					maxHeight = rep.Height
				}
			}
		}
	}

	// Process video tracks
	for _, period := range mpd.Periods {
		for _, as := range period.AdaptationSets {
			if !h.isVideo(as) {
				continue
			}
			for _, rep := range as.Representations {
				// Filter to highest quality only
				if rep.Height < maxHeight {
					continue
				}

				mediaURL := h.buildMediaPlaylistURL(proxyBaseURL, originalURL, rep.ID, headers, clearKey)

				inf := fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=%s", rep.Bandwidth)
				if rep.Width > 0 && rep.Height > 0 {
					inf += fmt.Sprintf(",RESOLUTION=%dx%d", rep.Width, rep.Height)
				}
				if rep.FrameRate != "" {
					inf += fmt.Sprintf(",FRAME-RATE=%s", rep.FrameRate)
				}
				if rep.Codecs != "" {
					inf += fmt.Sprintf(",CODECS=\"%s\"", rep.Codecs)
				}
				if hasAudio {
					inf += fmt.Sprintf(",AUDIO=\"%s\"", audioGroupID)
				}

				lines = append(lines, inf, mediaURL)
			}
		}
	}

	return strings.Join(lines, "\n"), nil
}

// convertMediaPlaylist generates an HLS media playlist for a specific representation.
func (h *MPDHandler) convertMediaPlaylist(manifest []byte, repID, proxyBaseURL, originalURL string, headers map[string]string, clearKey string) (string, error) {
	mpd, err := h.parseMPD(manifest)
	if err != nil {
		return "", err
	}

	// Find the representation
	var rep *Representation
	var as *AdaptationSet
	for _, period := range mpd.Periods {
		for i := range period.AdaptationSets {
			for j := range period.AdaptationSets[i].Representations {
				if period.AdaptationSets[i].Representations[j].ID == repID {
					rep = &period.AdaptationSets[i].Representations[j]
					as = &period.AdaptationSets[i]
					break
				}
			}
		}
	}

	if rep == nil {
		return "#EXTM3U\n#EXT-X-ERROR: Representation not found", nil
	}

	isLive := strings.ToLower(mpd.Type) == "dynamic"

	var lines []string
	lines = append(lines, "#EXTM3U", "#EXT-X-VERSION:3")

	if isLive {
		lines = append(lines, "#EXT-X-START:TIME-OFFSET=-30.0,PRECISE=NO")
	} else {
		lines = append(lines, "#EXT-X-TARGETDURATION:10", "#EXT-X-PLAYLIST-TYPE:VOD")
	}

	// Get segment template (from representation or adaptation set)
	st := rep.SegmentTemplate
	if st == nil {
		st = as.SegmentTemplate
	}

	if st == nil {
		return "#EXTM3U\n#EXT-X-ERROR: No SegmentTemplate found", nil
	}

	timescale := 1
	if st.Timescale != "" {
		timescale, _ = strconv.Atoi(st.Timescale)
	}

	startNumber := 1
	if st.StartNumber != "" {
		startNumber, _ = strconv.Atoi(st.StartNumber)
	}

	// Resolve base URL
	baseURL := h.getBaseURL(mpd, originalURL)

	// Build segments from timeline
	segments := h.buildSegmentsFromTimeline(st, repID, rep.Bandwidth, timescale, startNumber)

	// For live: sliding window of last 20 segments
	if isLive && len(segments) > 20 {
		segments = segments[len(segments)-20:]
	}

	if len(segments) > 0 {
		// Calculate target duration from max segment duration
		maxDur := 0.0
		for _, seg := range segments {
			if seg.Duration > maxDur {
				maxDur = seg.Duration
			}
		}

		if isLive {
			// Calculate media sequence from first segment time
			mediaSeq := segments[0].Time / int64(segments[0].DurationTS)
			lines = append(lines, fmt.Sprintf("#EXT-X-TARGETDURATION:%d", int(maxDur)+1))
			lines = append(lines, fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d", mediaSeq))
		}
	}

	// Determine if we need server-side decryption (for TS remux)
	useDecrypt := clearKey != "" || true // Always use decrypt endpoint for TS remux

	// Build init segment URL
	initURL := ""
	if st.Initialization != "" {
		initPath := h.replaceTemplateVars(st.Initialization, repID, rep.Bandwidth, 0, 0)
		initURL = h.resolveURL(initPath, baseURL)
	}

	// Add segments
	for _, seg := range segments {
		lines = append(lines, fmt.Sprintf("#EXTINF:%.3f,", seg.Duration))

		segURL := h.resolveURL(seg.URL, baseURL)

		if useDecrypt {
			// Use decrypt endpoint for TS output
			proxyURL := h.buildDecryptURL(proxyBaseURL, segURL, initURL, headers, clearKey)
			lines = append(lines, proxyURL)
		} else {
			// Direct segment proxy
			proxyURL := h.buildSegmentProxyURL(proxyBaseURL, segURL, headers)
			lines = append(lines, proxyURL)
		}
	}

	if !isLive {
		lines = append(lines, "#EXT-X-ENDLIST")
	}

	return strings.Join(lines, "\n"), nil
}

type segment struct {
	URL        string
	Duration   float64
	DurationTS int
	Time       int64
	Number     int
}

func (h *MPDHandler) buildSegmentsFromTimeline(st *SegmentTemplate, repID, bandwidth string, timescale, startNumber int) []segment {
	var segments []segment

	if st.SegmentTimeline == nil {
		return segments
	}

	currentTime := int64(0)
	segmentNumber := startNumber

	for _, s := range st.SegmentTimeline.S {
		if s.T != "" {
			t, _ := strconv.ParseInt(s.T, 10, 64)
			currentTime = t
		}

		d, _ := strconv.Atoi(s.D)
		r := 0
		if s.R != "" {
			r, _ = strconv.Atoi(s.R)
		}

		duration := float64(d) / float64(timescale)

		// Repeat r+1 times
		for i := 0; i <= r; i++ {
			segPath := h.replaceTemplateVars(st.Media, repID, bandwidth, segmentNumber, currentTime)

			segments = append(segments, segment{
				URL:        segPath,
				Duration:   duration,
				DurationTS: d,
				Time:       currentTime,
				Number:     segmentNumber,
			})

			currentTime += int64(d)
			segmentNumber++
		}
	}

	return segments
}

func (h *MPDHandler) replaceTemplateVars(template, repID, bandwidth string, number int, time int64) string {
	result := template
	result = strings.ReplaceAll(result, "$RepresentationID$", repID)
	result = strings.ReplaceAll(result, "$Bandwidth$", bandwidth)
	result = strings.ReplaceAll(result, "$Number$", strconv.Itoa(number))
	result = strings.ReplaceAll(result, "$Time$", strconv.FormatInt(time, 10))
	return result
}

func (h *MPDHandler) getBaseURL(mpd *MPD, originalURL string) string {
	if len(mpd.BaseURLs) > 0 && mpd.BaseURLs[0] != "" {
		return mpd.BaseURLs[0]
	}
	// Use directory of original URL
	// Important: use string manipulation to preserve original URL encoding
	// (Go's url.Parse + Path modification + String() re-encodes special chars)
	queryIdx := strings.Index(originalURL, "?")
	if queryIdx > 0 {
		originalURL = originalURL[:queryIdx]
	}
	lastSlash := strings.LastIndex(originalURL, "/")
	if lastSlash > 0 {
		return originalURL[:lastSlash+1]
	}
	return originalURL
}

func (h *MPDHandler) resolveURL(urlStr string, base string) string {
	return urlutil.ResolveURL(urlStr, base)
}

func (h *MPDHandler) isVideo(as AdaptationSet) bool {
	return strings.Contains(as.MimeType, "video") || strings.Contains(as.ContentType, "video")
}

func (h *MPDHandler) isAudio(as AdaptationSet) bool {
	return strings.Contains(as.MimeType, "audio") || strings.Contains(as.ContentType, "audio")
}

func (h *MPDHandler) buildMediaPlaylistURL(proxyBaseURL, originalURL, repID string, headers map[string]string, clearKey string) string {
	u, _ := url.Parse(proxyBaseURL + "/proxy/hls/manifest.m3u8")
	q := u.Query()
	q.Set("d", originalURL)
	q.Set("format", "hls")
	q.Set("rep_id", repID)
	for k, v := range headers {
		q.Set("h_"+k, v)
	}
	if clearKey != "" {
		q.Set("clearkey", clearKey)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (h *MPDHandler) buildSegmentProxyURL(proxyBaseURL, segmentURL string, headers map[string]string) string {
	u, _ := url.Parse(proxyBaseURL + "/proxy/stream")
	q := u.Query()
	q.Set("url", segmentURL)
	for k, v := range headers {
		q.Set("h_"+k, v)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (h *MPDHandler) buildDecryptURL(proxyBaseURL, segmentURL, initURL string, headers map[string]string, clearKey string) string {
	u, _ := url.Parse(proxyBaseURL + "/decrypt/segment.ts")
	q := u.Query()
	q.Set("url", segmentURL)
	if initURL != "" {
		q.Set("init_url", initURL)
	}
	for k, v := range headers {
		q.Set("h_"+k, v)
	}

	// Parse clearkey and add key/key_id params
	// Supports formats:
	// - Single key: "KID:KEY"
	// - Multi-key: "KID1:KEY1,KID2:KEY2"
	if clearKey != "" {
		var kids, keys []string
		pairs := strings.Split(clearKey, ",")
		for _, pair := range pairs {
			if kv := strings.SplitN(pair, ":", 2); len(kv) == 2 {
				kids = append(kids, strings.TrimSpace(kv[0]))
				keys = append(keys, strings.TrimSpace(kv[1]))
			}
		}
		if len(kids) > 0 && len(keys) > 0 {
			q.Set("key_id", strings.Join(kids, ","))
			q.Set("key", strings.Join(keys, ","))
		}
	} else {
		// No key - use skip_decrypt for remux only
		q.Set("key_id", "00000000000000000000000000000000")
		q.Set("key", "00000000000000000000000000000000")
		q.Set("skip_decrypt", "1")
	}

	u.RawQuery = q.Encode()
	return u.String()
}

// parseMPD parses an MPD manifest into a structured format.
func (h *MPDHandler) parseMPD(data []byte) (*MPD, error) {
	// Add namespace if missing
	content := string(data)
	if !strings.Contains(content, "xmlns") {
		content = strings.Replace(content, "<MPD", `<MPD xmlns="urn:mpeg:dash:schema:mpd:2011"`, 1)
	}

	var mpd MPD
	if err := xml.Unmarshal([]byte(content), &mpd); err != nil {
		return nil, fmt.Errorf("failed to parse MPD: %w", err)
	}
	return &mpd, nil
}

// MPD XML structures
type MPD struct {
	XMLName  xml.Name `xml:"MPD"`
	Type     string   `xml:"type,attr"`
	BaseURLs []string `xml:"BaseURL"`
	Periods  []Period `xml:"Period"`
}

type Period struct {
	AdaptationSets []AdaptationSet `xml:"AdaptationSet"`
}

type AdaptationSet struct {
	MimeType        string           `xml:"mimeType,attr"`
	ContentType     string           `xml:"contentType,attr"`
	Lang            string           `xml:"lang,attr"`
	SegmentTemplate *SegmentTemplate `xml:"SegmentTemplate"`
	Representations []Representation `xml:"Representation"`
}

type Representation struct {
	ID              string           `xml:"id,attr"`
	Bandwidth       string           `xml:"bandwidth,attr"`
	Width           int              `xml:"width,attr"`
	Height          int              `xml:"height,attr"`
	FrameRate       string           `xml:"frameRate,attr"`
	Codecs          string           `xml:"codecs,attr"`
	SegmentTemplate *SegmentTemplate `xml:"SegmentTemplate"`
}

type SegmentTemplate struct {
	Timescale       string           `xml:"timescale,attr"`
	Initialization  string           `xml:"initialization,attr"`
	Media           string           `xml:"media,attr"`
	StartNumber     string           `xml:"startNumber,attr"`
	SegmentTimeline *SegmentTimeline `xml:"SegmentTimeline"`
}

type SegmentTimeline struct {
	S []SegmentTimelineS `xml:"S"`
}

type SegmentTimelineS struct {
	T string `xml:"t,attr"`
	D string `xml:"d,attr"`
	R string `xml:"r,attr"`
}

var _ interfaces.StreamHandler = (*MPDHandler)(nil)
