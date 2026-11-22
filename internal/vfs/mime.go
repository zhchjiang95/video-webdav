package vfs

import "mime"

// 注册常见视频/字幕/音频/歌词/图片的 MIME 类型。
// Windows 下 Go 会从注册表读取 MIME 映射，结果不可靠（例如 .ts 可能被识别为其它类型，
// .mkv/.flac 可能没有映射导致每次都要读文件头嗅探），这里统一覆盖。
func init() {
	types := map[string]string{
		// 视频
		".mkv":  "video/x-matroska",
		".mp4":  "video/mp4",
		".m4v":  "video/x-m4v",
		".avi":  "video/x-msvideo",
		".mov":  "video/quicktime",
		".wmv":  "video/x-ms-wmv",
		".flv":  "video/x-flv",
		".f4v":  "video/mp4",
		".webm": "video/webm",
		".ts":   "video/mp2t",
		".m2ts": "video/mp2t",
		".mts":  "video/mp2t",
		".mpg":  "video/mpeg",
		".mpeg": "video/mpeg",
		".vob":  "video/mpeg",
		".rmvb": "application/vnd.rn-realmedia-vbr",
		".rm":   "application/vnd.rn-realmedia",
		".3gp":  "video/3gpp",
		".iso":  "application/x-iso9660-image",
		// 字幕
		".srt": "application/x-subrip",
		".ass": "text/x-ssa",
		".ssa": "text/x-ssa",
		".vtt": "text/vtt",
		".sup": "application/octet-stream",
		".sub": "text/plain",
		".idx": "text/plain",
		// 音频
		".mp3":  "audio/mpeg",
		".flac": "audio/flac",
		".wav":  "audio/wav",
		".aac":  "audio/aac",
		".m4a":  "audio/mp4",
		".ogg":  "audio/ogg",
		".opus": "audio/opus",
		".wma":  "audio/x-ms-wma",
		".ape":  "audio/x-ape",
		".alac": "audio/mp4",
		".aiff": "audio/aiff",
		".aif":  "audio/aiff",
		".dsf":  "audio/x-dsf",
		".dff":  "audio/x-dff",
		".wv":   "audio/x-wavpack",
		".mka":  "audio/x-matroska",
		// 歌词与分轨
		".lrc": "text/plain; charset=utf-8",
		".cue": "text/plain; charset=utf-8",
		// 图片
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".png":  "image/png",
		".webp": "image/webp",
		".gif":  "image/gif",
		".bmp":  "image/bmp",
		".heic": "image/heic",
		".heif": "image/heif",
		".avif": "image/avif",
		".tif":  "image/tiff",
		".tiff": "image/tiff",
		".svg":  "image/svg+xml",
		// 元数据
		".nfo": "text/xml",
	}
	for ext, t := range types {
		_ = mime.AddExtensionType(ext, t)
	}
}
