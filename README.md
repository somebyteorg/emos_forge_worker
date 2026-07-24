# emos-forge-worker

EMOS forge 媒体处理 worker。它负责从本地路径或 HTTP/HTTPS 地址读取视频，生成视频、音频、字幕、雪碧图等产物，写入
`manifest.json` 和 `log.json`，并在 worker 模式下上传后台指定的资源。

## 编译

需要 Go 环境。项目只面向 Linux 运行，根目录提供了编译脚本：

```bash
./build.sh
```

编译完成后会在当前项目目录生成：

```text
./forge-worker
```

查看版本：

```bash
./forge-worker -v
```

## 运行依赖

### 包管理安装

#### Alpine 3.23

```bash
apk add ffmpeg vips vips-heif vips-tools vips-magick libheif libheif-tools
```

#### Debian 13

```bash
apt install ffmpeg libvips-tools libheif1 libheif-dev libheif-examples libheif-plugin-x265 libheif-plugin-aomenc
```

### 安装 Shaka Packager

```bash
wget https://github.com/shaka-project/shaka-packager/releases/download/v3.9.1/packager-linux-x64
chmod +x packager-linux-x64
mv packager-linux-x64 /usr/local/bin/packager
```

## 配置

复制示例配置：

```bash
cp .env.example .env
```

worker 模式必填：

```env
EMOS_URL=https://emos.best
EMOS_TOKEN=
EMOS_FORGE_WORKER_ID=
```

## 使用

检查运行环境：

```bash
./forge-worker doctor
```

启动 worker：

```bash
./forge-worker worker
```

只处理一个任务后退出，适合测试：

```bash
./forge-worker worker --once
```

本地切片模式：

```bash
./forge-worker local
```

`local` 参数：

| 参数                    | 说明                                                                                                                                                                                            |
|-----------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `[input]` / `--input` | 输入视频本地绝对路径或 HTTP(S) URL。传入后不进入交互模式，其他未传字段使用默认值或 `.env` 配置。                                                                                                                                    |
| `--uuid`              | 指定任务 UUID；不传则自动生成。                                                                                                                                                                            |
| `--output`            | 输出根目录，最终产物目录为 `<output>/<uuid>`。默认 `./output`。                                                                                                                                                |
| `--video`             | 是否启用视频处理，默认 `true`；关闭用 `--video=false`。                                                                                                                                                       |
| `--video-profiles`    | 视频规则，逗号分隔。支持 `package`、`720p`、`1080p`、`2160p`；默认 `package`。                                                                                                                                   |
| `--audio`             | 是否启用音频处理，默认 `true`；关闭用 `--audio=false`。                                                                                                                                                       |
| `--audio-rules`       | 音频规则，逗号分隔。支持 `package`、`aac`、`none`；默认 `package,aac`。`package` 会保留适合 MP4/HLS 的 AAC、AC-3、E-AC-3 原始音轨；TrueHD、DTS、FLAC、Opus 等不兼容编码会直接转为 AAC LC。`package,aac` 会在原轨兼容时同时输出原轨和 AAC，两者都不是 `/tmp` 产物。 |
| `--audio-strategy`    | 音轨选择策略，默认 `one_per_language`。                                                                                                                                                                 |
| `--sprites`           | 是否生成雪碧图，默认 `true`；关闭用 `--sprites=false`。                                                                                                                                                      |
| `--sprite-sizes`      | 雪碧图尺寸，逗号分隔，默认 `1280x720,640x360,320x180`。                                                                                                                                                     |
| `--subtitles`         | 是否提取文本字幕，默认 `true`；关闭用 `--subtitles=false`。                                                                                                                                                   |
| `--encrypt`           | 是否对封装后的音视频开启 ClearKey，默认跟随配置；常用关闭方式是 `--encrypt=false`。                                                                                                                                       |

手动上传已有产物目录：

```bash
./forge-worker upload --job-uuid <forge_job_uuid> --root ./output/<forge_job_uuid>
```

`upload` 参数：

| 参数                   | 说明                                                                                                            |
|----------------------|---------------------------------------------------------------------------------------------------------------|
| `--job-uuid`         | EMOS forge job UUID；不传则进入交互输入。                                                                                |
| `--root`             | 已完成任务的输出目录，目录中需要包含 `manifest.json`；不传则进入交互输入。                                                                 |
| `--delete-artifacts` | 手动上传成功后删除已上传的任务文件并归档根目录 JSON，默认 `false`。这个参数只影响本次手动 `upload`，默认值不跟随 `.env` 里的 `EMOS_UPLOAD_DELETE_ARTIFACTS`。 |

如果不传 `--job-uuid` 或 `--root`，`upload` 会进入交互输入，并询问是否删除已上传文件，默认不删除。手动上传前会检查
`manifest.json`，如果包含未加密视频会拒绝上传。

## 默认处理规则

worker 模式下，后台通过 `job_steps` 指定要处理的内容：

| job step           | 处理内容                              |
|--------------------|-----------------------------------|
| `video_package`    | 保留原视频规格，remux 后封装                 |
| `video_720p`       | 生成 720p 视频                        |
| `video_1080p`      | 生成 1080p 视频                       |
| `audio_package`    | 选择并封装原始音轨                         |
| `audio_aac`        | 将选中的非 AAC 音轨转为 AAC；选中音轨已是 AAC 时跳过 |
| `subtitle_package` | 提取全部文本字幕并输出 WebVTT；图片字幕跳过         |
| `sprite_320`       | 生成 `320x180` 雪碧图                  |
| `sprite_640`       | 生成 `640x360` 雪碧图                  |
| `sprite_720`       | 生成 `1280x720` 雪碧图                 |

编码与产物规则：

| 类型  | 默认规则                                                                                                                                                                                                                                    |
|-----|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 视频  | 策略为 `source_aware`。所有视频任务先把源视频轨无损 remux 为 package 标准输入；只有明确请求 `package` 时才把它加入最终 HLS。生成档位从该标准输入读取，不会放大视频，源宽高都小于目标档位时会跳过。                                                                                                                |
| 音频  | 策略为 `one_per_language`。每种语言、每种角色选一条最佳音轨，优先默认音轨，其次声道数、码率、轨道序号。`audio_package` 保留兼容 MP4/HLS 的 AAC、AC-3、E-AC-3；TrueHD、DTS、FLAC、Opus 等直接转 AAC LC。额外请求 `audio_aac` 时，其余非 AAC 音轨也会转为 AAC LC。默认不包含 commentary 和 visual impaired，AAC 输出最大 6 声道。 |
| 字幕  | `local` 模式默认启用，worker 模式由 `subtitle_package` 启用。只处理文本字幕并输出 WebVTT，图片字幕会跳过。所有文本字幕会在一次 ffmpeg 输入中输出。                                                                                                                                      |
| 雪碧图 | 基于关键帧生成。从 3 秒后开始，按固定 `10s` 间隔选择最接近的关键帧。                                                                                                                                                                                                 |

视频生成档位：

| profile   | 编码规则                                                                                            |
|-----------|-------------------------------------------------------------------------------------------------|
| `package` | 输入视频 remux，不转码。                                                                                 |
| `720p`    | 最大 `1280x720`，SDR 输出 H.264 High / `yuv420p`，平均码率上限 `3.2 Mbps`，峰值 `4.5 Mbps`；HDR 输入转 SDR。        |
| `1080p`   | 最大 `1920x1080`，输出 HEVC Main/Main10，SDR 平均码率上限 `6 Mbps`，HDR 平均码率上限 `7 Mbps`，峰值 `10 Mbps`。        |
| `2160p`   | 代码支持最大 `3840x2160`，输出 HEVC，SDR 平均码率上限 `16 Mbps`，HDR 平均码率上限 `20 Mbps`；当前 worker 接口没有对应 job step。 |

720p 源帧率超过 30fps 时会减半输出。Dolby Vision Profile 7 以及带 HDR10 兼容基础层的 Profile 8.1 会先无损移除 RPU/增强层，以标准
HDR10 基础层进入后续封装、转码和雪碧图流程；没有 HDR10 兼容基础层的 Dolby Vision 仍会拒绝处理。

## 输出目录

默认输出到：

```text
./output
```

每个任务会生成独立目录，例如：

```text
output/<job_uuid>/
  manifest.json
  log.json
  upload_state.json
  tmp/                 # worker 未收到 completed 成功响应前保留
    pipeline_state.json
```
