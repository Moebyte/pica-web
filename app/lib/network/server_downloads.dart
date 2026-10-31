/// Server-side download client (web only).
///
/// Web 端没有文件系统，原生下载不可用。浏览器用现有源逻辑解析出每页
/// 图片 URL + 请求头，提交给网关下载到服务器磁盘；"已下载"改为浏览
/// 服务器上已下载的漫画（见 `pages/server_downloads_page.dart`）。
///
/// 网关契约（同源，浏览器自动携带 cookie）：
/// - `POST   /api/downloads/jobs`             提交任务（幂等）
/// - `GET    /api/downloads/jobs`             任务列表
/// - `DELETE /api/downloads/jobs?id=&files=`  删除任务
/// - `GET    /api/downloads/file?path=<相对路径>`  取文件（支持 Range）
///
/// 所有请求都走 `createPicaDio()`：同源相对/绝对 URL 会自动绕过隧道，
/// 由浏览器 adapter 直接发出。
library;

import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:pica_comic/comic_source/comic_source.dart';
import 'package:pica_comic/foundation/def.dart';
import 'package:pica_comic/foundation/log.dart';
import 'package:pica_comic/network/app_dio.dart';
import 'package:pica_comic/network/base_comic.dart';
import 'package:pica_comic/network/res.dart';
import 'package:pica_comic/components/components.dart';
import 'package:pica_comic/tools/translations.dart';

import 'hitomi_network/hitomi_models.dart';
import 'hitomi_network/image.dart' as hitomi_img;
import 'htmanga_network/htmanga_main_network.dart' as ht_net;
import 'jm_network/headers.dart' as jm_api;
import 'jm_network/jm_network.dart' as jm_net;
import 'nhentai_network/nhentai_main_network.dart' as nh_net;
import 'picacg_network/methods.dart' as picacg_net;

/// 服务端下载任务提交器 / 网关 API 客户端。
///
/// 仅在 Web（kIsWeb）使用；原生下载链路不受影响。
class ServerDownloads {
  ServerDownloads._();

  static Dio? _dio;

  static Dio get client => _dio ??= createPicaDio(BaseOptions(
        connectTimeout: const Duration(seconds: 10),
        receiveTimeout: const Duration(seconds: 60),
        // 404 等状态用于探测（如 job.json），不应直接抛异常
        validateStatus: (status) => status != null && status < 500,
      ));

  static Uri _api(String pathAndQuery) => Uri.base.resolve(pathAndQuery);

  // ---------------------------------------------------------------------
  // 任务提交
  // ---------------------------------------------------------------------

  /// 与阅读器保持一致：JM 的 scramble_id 全站固定为 220980
  /// （见 `pages/reader/reading_data.dart` 与
  /// `foundation/image_manager.dart` 的 JM 分支，`JmNetwork.getScramble`
  /// 虽存在但从未被使用）。
  static const String _jmScrambleId = "220980";

  /// 提交一个章节到服务端下载队列。
  ///
  /// [ep] 为章节标识：JM 是章节 id（epsId），picacg 是章节序号（从 1
  /// 开始），自定义源是章节 id；单章源传空字符串。
  ///
  /// [comicInfo] 为源特有的完整数据（如 hitomi 的 [HitomiComic]，
  /// 需要其中的 files 才能构造图片 URL）。
  ///
  /// [pageUrls] 允许调用方传入已解析好的页面 URL（如确认对话框中已经
  /// 解析过一次，避免重复请求）。
  static Future<Res<bool>> submit(
    ComicSource source,
    BaseComic comic,
    String ep,
    String epName, {
    Object? comicInfo,
    List<String>? pageUrls,
  }) async {
    final hasEp = ep.isNotEmpty;
    final epValue = hasEp ? ep : "0";
    final jobId =
        hasEp ? "${source.key}-${comic.id}-$ep" : "${source.key}-${comic.id}";

    // 1. 解析页面 URL
    List<String> urls;
    if (pageUrls != null) {
      urls = pageUrls;
    } else {
      var urlsRes = await _loadPageUrls(source, comic, ep, comicInfo);
      if (urlsRes.error) {
        return Res.error("解析图片链接失败: ${urlsRes.errorMessage}");
      }
      urls = urlsRes.data;
    }
    if (urls.isEmpty) {
      return const Res.error("未解析到任何图片链接");
    }

    // 2. 构造页面列表（url + 归一化后的 headers，JM 附加重组参数）
    var pages = <Map<String, dynamic>>[];
    for (var url in urls) {
      var (headers, requestUrl) = _pageRequest(source, comic, ep, url);
      var page = <String, dynamic>{
        "url": requestUrl,
        "headers": headers,
      };
      if (source.key == "jm") {
        var jmInfo = _buildJmInfo(url, epsId: hasEp ? ep : comic.id);
        if (jmInfo != null) {
          page["jm"] = jmInfo;
        }
      }
      pages.add(page);
    }

    // 3. 提交
    var body = <String, dynamic>{
      "id": jobId,
      "title": comic.title,
      "source": source.key,
      "comicId": comic.id,
      "ep": epValue,
      "epName": epName,
      "pages": pages,
      "createdAt": DateTime.now().millisecondsSinceEpoch ~/ 1000,
    };

    try {
      var res = await client.post(
        _api("/api/downloads/jobs").toString(),
        data: jsonEncode(body),
        options:
            Options(headers: {Headers.contentTypeHeader: "application/json"}),
      );
      if (res.statusCode != null && res.statusCode! < 300) {
        log("Submitted server download job $jobId (${pages.length} pages)",
            "ServerDownloads");
        return const Res(true);
      }
      return Res.error("网关返回 ${res.statusCode}");
    } catch (e) {
      log("Failed to submit server download job $jobId: $e", "ServerDownloads",
          LogLevel.error);
      return Res.error("提交失败: $e");
    }
  }

  /// 解析某章节的所有页面 URL。
  ///
  /// 自定义（JS）源通过 `source.loadComicPages` 获取；内置源没有实现该
  /// 回调，按源走各自的网络层（与阅读器 `ReadingData.loadEpNetwork`
  /// 相同的路径）。
  static Future<Res<List<String>>> _loadPageUrls(
      ComicSource source, BaseComic comic, String ep, Object? comicInfo) async {
    if (source.loadComicPages != null) {
      return await source.loadComicPages!(comic.id, ep.isEmpty ? null : ep);
    }
    switch (source.key) {
      case "jm":
        return await jm_net.JmNetwork().getChapter(
          ep.isEmpty ? comic.id : ep,
        );
      case "picacg":
        // 章节标识是序号（从 1 开始）；单章/拿不到序号时退回第 1 章
        var order = int.tryParse(ep) ?? 1;
        if (order < 1) {
          order = 1;
        }
        return await picacg_net.PicacgNetwork()
            .getComicContent(comic.id, order);
      case "nhentai":
        return await nh_net.NhentaiNetwork().getImages(comic.id);
      case "htmanga":
        return await ht_net.HtmangaNetwork().getImages(comic.id);
      case "hitomi":
        var comicFull = comicInfo is HitomiComic ? comicInfo : null;
        if (comicFull == null || comicFull.files.isEmpty) {
          return const Res.error("hitomi: 缺少图片列表");
        }
        try {
          var gg = hitomi_img.GG();
          var urls = <String>[];
          for (var file in comicFull.files) {
            urls.add(
                await gg.urlFromUrlFromHash(comicFull.id, file, 'webp', null));
          }
          return Res(urls);
        } catch (e) {
          return Res.error("hitomi: 解析图片链接失败: $e");
        }
      case "ehentai":
        return const Res.error(
            "EHentai 的图片链接需要逐页通过 API 解析，暂不支持服务端下载");
    }
    return Res.error("源 \"${source.key}\" 无法解析页面链接");
  }

  /// 计算某一页的请求头与实际请求地址。
  ///
  /// 自定义源的 `getImageLoadingConfig` 可能返回不同的 `url`（如 POST
  /// 接口），网关契约只支持 GET：此时用配置中的 url 并记 warning。
  static (Map<String, String>, String) _pageRequest(
      ComicSource source, BaseComic comic, String ep, String url) {
    if (source.getImageLoadingConfig != null) {
      var config = source.getImageLoadingConfig!.call(url, comic.id, ep);
      var method = config["method"];
      if (method != null && method.toString().toUpperCase() != "GET") {
        log(
            "ServerDownloads: source \"${source.key}\" uses $method to load "
            "images; the gateway contract only supports GET.",
            "ServerDownloads",
            LogLevel.warning);
      }
      if (config["data"] != null) {
        log(
            "ServerDownloads: source \"${source.key}\" needs request body to "
            "load images, which the gateway contract does not support.",
            "ServerDownloads",
            LogLevel.warning);
      }
      var configUrl = config["url"];
      var headers = _normalizeHeaders(config["headers"]) ?? {};
      return (
        headers,
        configUrl is String && configUrl.isNotEmpty ? configUrl : url
      );
    }
    return (_defaultHeaders(source, comic, url), url);
  }

  /// `Map<String, dynamic>` -> `Map<String, String>`（跳过 null 值）
  static Map<String, String>? _normalizeHeaders(dynamic headers) {
    if (headers is! Map) {
      return null;
    }
    var res = <String, String>{};
    headers.forEach((key, value) {
      if (value == null) {
        return;
      }
      res[key.toString()] = value is Iterable
          ? value.where((e) => e != null).map((e) => e.toString()).join(", ")
          : value.toString();
    });
    return res;
  }

  /// 内置源的默认图片请求头（与阅读器加载图片时一致）
  static Map<String, String> _defaultHeaders(
      ComicSource source, BaseComic comic, String url) {
    switch (source.key) {
      case "jm":
        return jm_api.getImgHeaders();
      case "hitomi":
        return {
          "User-Agent": webUA,
          "Referer": "https://hitomi.la/reader/${comic.id}.html",
        };
      default:
        return {"User-Agent": webUA};
    }
  }

  /// JM 图片的重组参数。
  ///
  /// - `epsId`: 章节 id（单章漫画即漫画 id）；
  /// - `scrambleId`: 全站固定 "220980"（与阅读器一致）；
  /// `bookId`: 图片文件名去掉扩展名（URL 最后一段，从第一个 `.` 截断，
  ///   与 `ImageManager.getJmImage` 内的 `replaceAll(RegExp(r"\..+"), "")`
  ///   一致）。
  ///
  /// 字段拿不到时返回 null（省略 jm 字段并记 warning，网关会原样保存）。
  static Map<String, String>? _buildJmInfo(String url,
      {required String epsId}) {
    var bookId = _jmBookId(url);
    if (epsId.isEmpty || bookId == null || _jmScrambleId.isEmpty) {
      log(
          "ServerDownloads: failed to build jm info for $url "
          "(epsId: $epsId, bookId: $bookId); the page is submitted "
          "without recombine params.",
          "ServerDownloads",
          LogLevel.warning);
      return null;
    }
    return {
      "epsId": epsId,
      "scrambleId": _jmScrambleId,
      "bookId": bookId,
    };
  }

  static String? _jmBookId(String url) {
    var path = url.split("?").first;
    var segments = path.split("/");
    if (segments.isEmpty || segments.last.isEmpty) {
      return null;
    }
    var segment = segments.last;
    var dot = segment.indexOf(".");
    if (dot <= 0) {
      return segment;
    }
    return segment.substring(0, dot);
  }

  // ---------------------------------------------------------------------
  // 网关 API
  // ---------------------------------------------------------------------

  /// 获取任务列表
  static Future<Res<List<ServerDownloadJob>>> getJobs() async {
    try {
      var res = await client.get(_api("/api/downloads/jobs").toString());
      if (res.statusCode != 200) {
        return Res.error("网关返回 ${res.statusCode}");
      }
      var data = res.data;
      if (data is String) {
        data = jsonDecode(data);
      }
      var jobs = <ServerDownloadJob>[];
      for (var job in ((data as Map)["jobs"] as List? ?? [])) {
        if (job is Map) {
          jobs.add(ServerDownloadJob.fromJson(Map<String, dynamic>.from(job)));
        }
      }
      return Res(jobs);
    } catch (e) {
      return Res.error(e.toString());
    }
  }

  /// 删除任务。[files] 为 true 时连同已下载文件一起删除。
  static Future<Res<bool>> deleteJob(String id, {bool files = false}) async {
    try {
      var res = await client.delete(_api(
        "/api/downloads/jobs?id=${Uri.encodeQueryComponent(id)}"
        "&files=${files ? "true" : "false"}",
      ).toString());
      if (res.statusCode != null && res.statusCode! < 300) {
        return const Res(true);
      }
      return Res.error("网关返回 ${res.statusCode}");
    } catch (e) {
      return Res.error(e.toString());
    }
  }

  /// 任务 dir 下某个文件的访问 URL
  static String fileUrl(String dir, String relativePath) {
    var path = dir.isEmpty
        ? relativePath
        : (dir.endsWith("/") ? "$dir$relativePath" : "$dir/$relativePath");
    return _api(
            "/api/downloads/file?path=${Uri.encodeQueryComponent(path)}")
        .toString();
  }

  /// 探测任务 dir 下的一个文件（Range: bytes=0-0），返回是否存在。
  static Future<bool> probeFile(String dir, String relativePath) async {
    try {
      var res = await client.get(
        fileUrl(dir, relativePath),
        options: Options(
          headers: {"Range": "bytes=0-0"},
          responseType: ResponseType.bytes,
        ),
      );
      return res.statusCode == 200 || res.statusCode == 206;
    } catch (_) {
      return false;
    }
  }

  /// 读取任务 dir 下的 job.json，解析文件名列表。
  ///
  /// 兼容两种形态：
  /// - `{"files": ["001.jpg", ...]}`（文件名清单）；
  /// - 网关把提交的完整 job body 原样落盘（含 `pages`），此时从每页
  ///   URL 的扩展名推导文件名。
  static Future<List<String>?> readJobManifest(String dir) async {
    try {
      var res = await client.get(
        fileUrl(dir, "job.json"),
        options: Options(responseType: ResponseType.plain),
      );
      if (res.statusCode != 200 || res.data is! String) {
        return null;
      }
      var json = jsonDecode(res.data as String);
      if (json is! Map) {
        return null;
      }
      var files = json["files"];
      if (files is List && files.isNotEmpty) {
        return files.map((e) => e.toString()).toList();
      }
      var pages = json["pages"];
      if (pages is List && pages.isNotEmpty) {
        var names = <String>[];
        for (var i = 0; i < pages.length; i++) {
          var url = pages[i] is Map ? pages[i]["url"] : null;
          names.add(_pageFileName(i, url is String ? url : ""));
        }
        return names;
      }
      return null;
    } catch (_) {
      return null;
    }
  }

  /// `<序号3位>.<ext>`，扩展名取自页面 URL（默认 jpg）
  static String _pageFileName(int index, String url) {
    var ext = "jpg";
    var segment = url.split("?").first.split("/").last;
    var dot = segment.lastIndexOf(".");
    if (dot > 0 && dot < segment.length - 1) {
      var candidate = segment.substring(dot + 1).toLowerCase();
      if (candidate.length <= 5 && RegExp(r"^[a-z0-9]+$").hasMatch(candidate)) {
        ext = candidate;
      }
    }
    return "${(index + 1).toString().padLeft(3, '0')}.$ext";
  }

  // ---------------------------------------------------------------------
  // 下载入口（详情页“下载”按钮在 Web 下的行为）
  // ---------------------------------------------------------------------

  /// Web 端下载入口：弹确认对话框（标题/页数/章节）后提交任务。
  ///
  /// [chapters] 为章节列表 `(章节标识, 章节名)`；null 表示单章源。
  /// [currentChapter] 为“当前章节”选项的序号（从 1 开始，通常取自阅读
  /// 历史，默认 1）。
  static Future<void> startWebDownload(
    BuildContext context, {
    required ComicSource source,
    required BaseComic comic,
    required List<(String, String)>? chapters,
    int currentChapter = 1,
    Object? comicInfo,
  }) async {
    var clampedCurrent =
        currentChapter < 1 ? 1 : currentChapter;
    if (chapters != null && chapters.isNotEmpty) {
      if (clampedCurrent > chapters.length) {
        clampedCurrent = 1;
      }
    }

    var previewEp = chapters == null || chapters.isEmpty
        ? ""
        : chapters[clampedCurrent - 1].$1;
    var previewEpName = chapters == null || chapters.isEmpty
        ? ""
        : chapters[clampedCurrent - 1].$2;

    // 预解析“当前章节”的页数，用于对话框展示
    var previewUrls = <String>[];
    String? previewError;
    var previewResolved = false;

    var future = _loadPageUrls(source, comic, previewEp, comicInfo).then((res) {
      if (res.success) {
        previewUrls = res.data;
      } else {
        previewError = res.errorMessage;
      }
    }, onError: (Object e) {
      previewError = e.toString();
    }).whenComplete(() => previewResolved = true);

    if (context.mounted) {
      await showDialog(
        context: context,
        builder: (dialogContext) => _ServerDownloadDialog(
          source: source,
          comic: comic,
          chapters: chapters,
          currentChapter: clampedCurrent,
          comicInfo: comicInfo,
          previewEp: previewEp,
          previewEpName: previewEpName,
          previewResolved: () => previewResolved,
          previewUrls: () => previewUrls,
          previewError: () => previewError,
          previewFuture: future,
        ),
      );
    }
  }
}

/// 服务端下载任务
class ServerDownloadJob {
  final String id;
  final String title;
  final String source;
  final String comicId;
  final String ep;
  final String epName;
  final int total;
  final int done;
  final int failed;
  final String status;
  final String dir;

  /// 已成功下载的文件名（相对 dir，按页序）；网关较老时为空
  final List<String> files;

  /// queued / running / done / partial / error
  bool get isBrowsable => status == "done" || status == "partial";

  String get statusText => switch (status) {
        "queued" => "排队中",
        "running" => "下载中",
        "done" => "已完成",
        "partial" => "部分完成",
        "error" => "失败",
        _ => status,
      }.tl;

  const ServerDownloadJob({
    required this.id,
    required this.title,
    required this.source,
    required this.comicId,
    required this.ep,
    required this.epName,
    required this.total,
    required this.done,
    required this.failed,
    required this.status,
    required this.dir,
    this.files = const [],
  });

  factory ServerDownloadJob.fromJson(Map<String, dynamic> json) {
    return ServerDownloadJob(
      id: json["id"]?.toString() ?? "",
      title: json["title"]?.toString() ?? "",
      source: json["source"]?.toString() ?? "",
      comicId: json["comicId"]?.toString() ?? "",
      ep: json["ep"]?.toString() ?? "0",
      epName: json["epName"]?.toString() ?? "",
      total: (json["total"] as num?)?.toInt() ?? 0,
      done: (json["done"] as num?)?.toInt() ?? 0,
      failed: (json["failed"] as num?)?.toInt() ?? 0,
      status: json["status"]?.toString() ?? "queued",
      dir: json["dir"]?.toString() ?? "",
      files: (json["files"] as List? ?? [])
          .map((e) => e.toString())
          .toList(growable: false),
    );
  }
}

/// 确认对话框：展示标题/页数/章节选择，确认后提交。
class _ServerDownloadDialog extends StatefulWidget {
  const _ServerDownloadDialog({
    required this.source,
    required this.comic,
    required this.chapters,
    required this.currentChapter,
    required this.comicInfo,
    required this.previewEp,
    required this.previewEpName,
    required this.previewResolved,
    required this.previewUrls,
    required this.previewError,
    required this.previewFuture,
  });

  final ComicSource source;
  final BaseComic comic;
  final List<(String, String)>? chapters;
  final int currentChapter;
  final Object? comicInfo;
  final String previewEp;
  final String previewEpName;
  final bool Function() previewResolved;
  final List<String> Function() previewUrls;
  final String? Function() previewError;
  final Future<void> previewFuture;

  @override
  State<_ServerDownloadDialog> createState() => _ServerDownloadDialogState();
}

class _ServerDownloadDialogState extends State<_ServerDownloadDialog> {
  bool _downloadAll = true;
  bool _submitting = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    widget.previewFuture.whenComplete(() {
      if (mounted) {
        setState(() {});
      }
    });
  }

  Future<void> _submit() async {
    setState(() {
      _submitting = true;
      _error = null;
    });

    var chapters = widget.chapters;
    var targets = <(String, String)>[];
    if (chapters == null || chapters.isEmpty) {
      targets.add((widget.previewEp, widget.previewEpName));
    } else if (_downloadAll) {
      targets.addAll(chapters);
    } else {
      targets.add(chapters[widget.currentChapter - 1]);
    }

    var successCount = 0;
    String? firstError;
    for (var (ep, epName) in targets) {
      // 预解析的“当前章节”结果直接复用，避免重复请求
      List<String>? preloaded;
      if (ep == widget.previewEp && widget.previewResolved()) {
        preloaded = widget.previewUrls();
      }
      var res = await ServerDownloads.submit(
        widget.source,
        widget.comic,
        ep,
        epName,
        comicInfo: widget.comicInfo,
        pageUrls: preloaded,
      );
      if (res.success) {
        successCount++;
      } else {
        firstError ??= res.errorMessage;
      }
    }

    if (!mounted) {
      return;
    }
    Navigator.of(context).pop();
    if (successCount > 0) {
      showToast(message: "已加入服务端下载队列".tl);
    } else {
      showToast(message: "提交失败: ${firstError ?? "未知错误"}".tl);
    }
  }

  @override
  Widget build(BuildContext context) {
    var chapters = widget.chapters;
    var multiChapter = chapters != null && chapters.isNotEmpty;

    Widget pageCounter;
    if (!widget.previewResolved()) {
      pageCounter = Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          const SizedBox(
            width: 14,
            height: 14,
            child: CircularProgressIndicator(strokeWidth: 2),
          ),
          const SizedBox(width: 8),
          Text("正在解析页数...".tl),
        ],
      );
    } else if (widget.previewError() != null || widget.previewUrls().isEmpty) {
      pageCounter = Text(
        "页数解析失败".tl,
        style: TextStyle(color: Theme.of(context).colorScheme.error),
      );
    } else {
      pageCounter = Text(
        "${multiChapter ? "当前章节共" : "共"} ${widget.previewUrls().length} 页".tl,
      );
    }

    return AlertDialog(
      title: Text("服务端下载".tl),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            widget.comic.title,
            style: const TextStyle(fontWeight: FontWeight.bold),
          ),
          const SizedBox(height: 8),
          if (!multiChapter && widget.previewEpName.isNotEmpty) ...[
            Text(widget.previewEpName),
            const SizedBox(height: 8),
          ],
          pageCounter,
          if (multiChapter) ...[
            const SizedBox(height: 12),
            Text("下载范围".tl),
            ListTile(
              leading: Icon(
                _downloadAll
                    ? Icons.radio_button_checked
                    : Icons.radio_button_off,
              ),
              title: Text("全部章节（${chapters.length} 章）".tl),
              contentPadding: EdgeInsets.zero,
              dense: true,
              onTap: _submitting ? null : () => setState(() => _downloadAll = true),
            ),
            ListTile(
              leading: Icon(
                !_downloadAll
                    ? Icons.radio_button_checked
                    : Icons.radio_button_off,
              ),
              title: Text(
                "仅当前章节（第 ${widget.currentChapter} 章 "
                "${chapters[widget.currentChapter - 1].$2}）".tl,
              ),
              contentPadding: EdgeInsets.zero,
              dense: true,
              onTap: _submitting ? null : () => setState(() => _downloadAll = false),
            ),
          ],
          if (_error != null) ...[
            const SizedBox(height: 8),
            Text(
              _error!,
              style: TextStyle(color: Theme.of(context).colorScheme.error),
            ),
          ],
        ],
      ),
      actions: [
        TextButton(
          onPressed:
              _submitting ? null : () => Navigator.of(context).pop(),
          child: Text("取消".tl),
        ),
        FilledButton(
          onPressed: _submitting ? null : _submit,
          child: _submitting
              ? const SizedBox(
                  width: 16,
                  height: 16,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : Text("下载".tl),
        ),
      ],
    );
  }
}
