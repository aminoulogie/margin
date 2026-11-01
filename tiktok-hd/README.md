# TikTok HD (iOS)

Not part of Margin: a small iPhone app kept on this side branch.

- **Patch** tab: pick a video (Photos, original file, or Files). It's copied without re-encoding
  (AVFoundation passthrough) and given the mvhd patch (same as `tiktok_prep.py --patch`), then
  checked: same codec, size, fps, HDR, Dolby Vision, frame count. Saved in Files >
  On My iPhone > TikTok HD.
- **TikTok** tab: TikTok in the app. tiktok.com is the mobile site; TikTok Studio opens as the
  desktop site scaled to the screen (WebKit's desktop mode), with a switch in the ⋯ menu.

Build: GitHub Actions (`.github/workflows/tiktok-hd-ipa.yml`) makes an unsigned `TikTokHD.ipa`
artifact; sign and install it with Sideloadly on Windows (free Apple ID: re-install every 7 days).
