import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';
import 'package:pica_comic/web_compat/jscore.dart';
import 'package:html/parser.dart';
import 'package:pica_comic/comic_source/comic_source.dart';
import 'package:pica_comic/foundation/cache_manager.dart';
import 'package:pica_comic/foundation/image_loader/image_recombine.dart';
import 'package:pica_comic/foundation/log.dart';
import 'package:pica_comic/network/app_dio.dart';
import 'package:pica_comic/network/cookie_jar.dart';
import 'package:pica_comic/network/eh_network/eh_models.dart';
import 'package:pica_comic/network/eh_network/get_gallery_id.dart';
import 'package:pica_comic/network/hitomi_network/hitomi_models.dart';
import 'package:pica_comic/tools/extensions.dart';
import 'package:pica_comic/tools/file_type.dart';

import '../base.dart';
import '../network/eh_network/eh_main_network.dart';
import '../network/hitomi_network/image.dart';
import '../network/jm_network/headers.dart';
import '../network/res.dart';

class BadRequestException {
  final String message;

  const BadRequestException(this.message);

  @override
  String toString() => message;
}

class ImageManager {
  static ImageManager? cache;

  ///用于标记正在加载的项目, 避免出现多个异步函数加载同一张图片
  static Map<String, DownloadProgress> loadingItems = {};

  /// Image cache manager for reader and download manager
  factory ImageManager() => cache ??= ImageManager._create();

  static bool get haveTask => loadingItems.isNotEmpty;

  static void clearTasks() {
    loadingItems.clear();
  }

  ImageManager._create();

  final dio = logDio(BaseOptions())
    ..interceptors.add(CookieManagerSql(SingleInstanceCookieJar.instance!));

  /// Web only: Dio used to call the same-origin `/api/img/sign` endpoint.
  /// `createPicaDio` installs the browser adapter for same-origin URLs.
  static Dio? _signDio;

  static Dio get _signClient => _signDio ??= createPicaDio(BaseOptions(
      connectTimeout: const Duration(seconds: 10),
      receiveTimeout: const Duration(seconds: 20)));

  /// Signed image URLs, keyed by the original URL and every signing input.
  /// The gateway derives a deterministic token per (url, headers, profile,
  /// jm) inside its expiry window, so this cache mostly avoids the extra
  /// round trip; it also keeps the browser cache key stable.
  static final Map<String, _SignedImage> _signedImages = {};

  static const int _signedImageLimit = 512;

  /// Headers that must never be copied into a signed URL: browsers own
  /// Cookie/Connection/hop-by-hop state and the gateway cookie vault injects
  /// origin cookies itself. `Accept-Encoding` is negotiated by the gateway's
  /// HTTP client. Everything else is replayed by the gateway from the token.
  static Map<String, String> signableImageHeaders(
      Map<String, String> headers) {
    final out = <String, String>{};
    headers.forEach((name, value) {
      final lower = name.toLowerCase();
      const blocked = {
        "cookie",
        "host",
        "content-length",
        "accept-encoding",
        "connection",
        "keep-alive",
        "te",
        "trailer",
        "transfer-encoding",
        "upgrade",
      };
      if (blocked.contains(lower) || lower.startsWith("proxy-")) {
        return;
      }
      out[name] = value;
    });
    return out;
  }

  static String _signedImageKey(String url, Map<String, String> headers,
      String? profile, Map<String, String>? jm) {
    final entries = headers.entries.toList()
      ..sort((a, b) => a.key.toLowerCase().compareTo(b.key.toLowerCase()));
    return jsonEncode([
      url,
      profile,
      jm,
      [
        for (final entry in entries) [entry.key.toLowerCase(), entry.value]
      ],
    ]);
  }

  /// Signs [url] through `POST /api/img/sign`. Returns null on any failure so
  /// callers can fall back to the legacy tunnel path.
  Future<String?> _signImageUrl(String url, Map<String, String> headers,
      {String? profile, Map<String, String>? jm}) async {
    if (!kIsWeb) {
      return null;
    }
    final key = _signedImageKey(
        url, signableImageHeaders(headers), profile, jm);
    final now = DateTime.now();
    final cached = _signedImages[key];
    if (cached != null &&
        cached.expiresAt.isAfter(now.add(const Duration(minutes: 2)))) {
      return cached.path;
    }
    try {
      final body = <String, dynamic>{"url": url, "ttl": 86400};
      final signable = signableImageHeaders(headers);
      if (signable.isNotEmpty) {
        body["headers"] = signable;
      }
      if (profile != null) {
        body["profile"] = profile;
      }
      if (jm != null) {
        body["jm"] = jm;
      }
      final res = await _signClient.post<dynamic>("/api/img/sign", data: body);
      final data = res.data;
      if (data is! Map || data["url"] is! String) {
        log("Invalid image sign response: $data", "Network", LogLevel.warning);
        return null;
      }
      final signed = data["url"] as String;
      var expiresAt = now.add(const Duration(hours: 1));
      final rawExpiry = data["expiresAt"];
      if (rawExpiry is String) {
        expiresAt =
            DateTime.tryParse(rawExpiry)?.toLocal() ?? expiresAt;
      }
      if (_signedImages.length >= _signedImageLimit) {
        _signedImages.remove(_signedImages.keys.first);
      }
      _signedImages[key] = _SignedImage(signed, expiresAt);
      return signed;
    } catch (e) {
      log("Failed to sign image URL, falling back to tunnel: $e", "Network",
          LogLevel.warning);
      return null;
    }
  }

  /// Web only: fetches an image through a signed same-origin GET so the
  /// browser and the gateway's disk cache can serve it. Returns null when the
  /// request cannot be signed or fails; the caller then uses the legacy tunnel.
  ///
  /// The fetch intentionally sends no headers: the upstream credentials and
  /// source headers are part of the signed token.
  Future<Response<ResponseBody>?> _getSignedImageResponse(String url,
      Map<String, String> headers,
      {String? profile, Map<String, String>? jm}) async {
    if (!kIsWeb) {
      return null;
    }
    final uri = Uri.tryParse(url);
    if (uri == null || (uri.scheme != "http" && uri.scheme != "https")) {
      return null;
    }
    final signed =
        await _signImageUrl(url, headers, profile: profile, jm: jm);
    if (signed == null) {
      return null;
    }
    try {
      return await dio.get<ResponseBody>(signed,
          options: Options(
              responseType: ResponseType.stream,
              headers: const <String, String>{}));
    } catch (e) {
      // Drop the memoized URL: it may be a token the gateway now rejects
      // (rotated secret, clock jump). The next attempt signs again.
      _signedImages
          .remove(_signedImageKey(url, signableImageHeaders(headers), profile, jm));
      log("Signed image fetch failed, falling back to tunnel: $e", "Network",
          LogLevel.warning);
      return null;
    }
  }

  int ehgtLoading = 0;

  /// 获取图片, 适用于没有任何限制的图片链接
  Stream<DownloadProgress> getImage(final String url,
      [Map<String, String>? headers]) async* {
    await wait(url);
    loadingItems[url] = DownloadProgress(0, 1, url, "");
    CachingFile? caching;

    try {
      final key = url;
      var cache = await CacheManager().findCache(key);
      if (cache != null) {
        yield DownloadProgress(
            1, 1, url, cache, null, CacheManager().getType(key));
        loadingItems.remove(url);
        return;
      }

      final cachingFile = await CacheManager().openWrite(key);
      caching = cachingFile;
      final savePath = cachingFile.file.path;
      yield DownloadProgress(0, 100, url, savePath);
      headers = headers ?? {};
      headers["User-Agent"] ??= webUA;
      headers["Connection"] = "Keep-Alive";
      var realUrl = url;
      if (url.contains("s.exhentai.org")) {
        // s.exhentai.org 有严格的加载限制
        realUrl = url.replaceFirst("s.exhentai.org", "ehgt.org");
      }
      if (realUrl.contains("ehgt.org")) {
        if (ehgtLoading < 3) {
          ehgtLoading++;
          await Future.delayed(const Duration(milliseconds: 10));
        } else {
          while (ehgtLoading > 2) {
            await Future.delayed(const Duration(milliseconds: 200));
          }
          ehgtLoading++;
        }
      }
      var dioRes = await _getSignedImageResponse(realUrl, headers);
      dioRes ??= await dio.get<ResponseBody>(realUrl,
          options:
              Options(responseType: ResponseType.stream, headers: headers));
      if (dioRes.data == null) {
        throw Exception("Empty Data");
      }
      List<int> imageData = [];
      int? expectedBytes;
      try {
        expectedBytes =
            int.parse(dioRes.data!.headers["Content-Length"]![0]) + 1;
      } catch (e) {
        //忽略
      }
      await for (var res in dioRes.data!.stream) {
        imageData.addAll(res);
        await cachingFile.writeBytes(res);
        var progress = DownloadProgress(imageData.length,
            (expectedBytes ?? imageData.length + 1), url, savePath);
        yield progress;
        loadingItems[url] = progress;
      }
      await cachingFile.close();
      var ext = getExt(dioRes);
      CacheManager().setType(key, ext);
      yield DownloadProgress(
        imageData.length,
        imageData.length,
        url,
        savePath,
        Uint8List.fromList(imageData),
        ext,
      );
    } catch (e, s) {
      caching?.cancel();
      log("$e\n$s", "Network", LogLevel.error);
      if (e is DioException && e.type == DioExceptionType.badResponse) {
        var statusCode = e.response?.statusCode;
        if (statusCode != null && statusCode >= 400 && statusCode < 500) {
          throw BadRequestException(e.message.toString());
        }
      }
      rethrow;
    } finally {
      if (url.contains("ehgt.org") || url.contains("s.exhentai.org")) {
        ehgtLoading--;
      }
      loadingItems.remove(url);
    }
  }

  Stream<DownloadProgress> getEhImageNew(
      final Gallery gallery, final int page) async* {
    final galleryLink = gallery.link;
    final cacheKey = "$galleryLink$page";
    final gid = getGalleryId(galleryLink);

    // check whether this image is loading
    await wait(cacheKey);
    loadingItems[cacheKey] = DownloadProgress(0, 1, cacheKey, "");

    CachingFile? caching;

    try {
      final key = cacheKey;
      var cache = await CacheManager().findCache(key);
      if (cache != null) {
        yield DownloadProgress(
            1, 1, key, cache, null, CacheManager().getType(key));
        loadingItems.remove(key);
        return;
      }

      final cachingFile = await CacheManager().openWrite(key);
      caching = cachingFile;
      final savePath = cachingFile.file.path;
      yield DownloadProgress(0, 100, key, savePath);

      final options = BaseOptions(
          followRedirects: true,
          connectTimeout: const Duration(seconds: 8),
          receiveTimeout: const Duration(seconds: 20),
          headers: {"user-agent": webUA, "cookie": EhNetwork().cookiesStr});

      var dio = logDio(options);

      // Get imgKey
      final readerLink =
          (await EhNetwork().getReaderLink(galleryLink, page)).data;

      Future<void> getShowKey() async {
        while (gallery.auth!["showKey"] == "loading") {
          await Future.delayed(const Duration(milliseconds: 100));
        }
        if (gallery.auth!["showKey"] != null ||
            gallery.auth!["mpvKey"] != null) {
          return;
        }
        gallery.auth!["showKey"] = "loading";
        try {
          var res = await EhNetwork().request(readerLink);

          var html = parse(res.data);
          var script = html
              .querySelectorAll("script")
              .firstWhereOrNull((element) => element.text.contains("showkey"));
          if (script != null) {
            var match = RegExp(r'showkey="(.*?)"').firstMatch(script.text);
            final showKey = match!.group(1)!;
            gallery.auth!["showKey"] = showKey;
          } else {
            final script = html
                .querySelectorAll("script")
                .firstWhereOrNull((element) => element.text.contains("mpvkey"))
                ?.text;
            if (script == null) {
              throw Exception("Failed to get showKey or mpvkey");
            }
            var mpvKey = script
                .split(";")
                .firstWhere((element) => element.contains("mpvkey"));
            gallery.auth!["mpvKey"] = mpvKey.removeAllBlank
                .replaceFirst("varmpvkey=", "")
                .replaceAll('"', "");
            var imageListScript = script
                .split(";")
                .firstWhere((element) => element.contains("imagelist"))
                .removeAllBlank
                .replaceFirst("varimagelist=", "");
            gallery.auth!["imgKey"] =
                jsonDecode(imageListScript).map((e) => e["k"]).join(",");
            gallery.auth!.remove("showKey");
          }
        } catch (e) {
          gallery.auth!.remove("showKey");
          rethrow;
        }
      }

      await getShowKey();
      assert(
          gallery.auth?["showKey"] != null || gallery.auth?["mpvKey"] != null);

      yield DownloadProgress(0, 100, cacheKey, savePath);

      Response<ResponseBody>? res;

      var imgKey = readerLink.split('/')[4];

      int totalBytes = 0;
      List<int> data = [];

      if (gallery.auth?["mpvKey"] != null) {
        Future<(String image, String nl)> getImageFromApi([String? nl]) async {
          Res<String>? apiRes = await EhNetwork().apiRequest({
            "gid": int.parse(gid),
            "imgkey": gallery.auth!["imgKey"]!.split(',')[page - 1],
            "method": "imagedispatch",
            "page": page,
            "mpvkey": gallery.auth!["mpvKey"],
            if (nl != null) "nl": nl
          });
          var apiJson = const JsonDecoder().convert(apiRes.data);
          return (apiJson["i"].toString(), apiJson["s"].toString());
        }

        var (image, nl) = await getImageFromApi();
        int retryTimes = 0;
        while (res == null) {
          try {
            if (image == "") {
              throw "empty url";
            }
            res = await dio.get<ResponseBody>(image,
                options: Options(responseType: ResponseType.stream));
            if (res.data!.headers["Content-Type"]?[0] ==
                    "text/html; charset=UTF-8" ||
                res.data!.headers["content-type"]?[0] ==
                    "text/html; charset=UTF-8") {
              throw ImageExceedError();
            }
          } catch (e) {
            retryTimes++;
            if (retryTimes == 4) {
              throw "Failed to load image.\nMaximum number of retries reached.";
            }
            (image, nl) = await getImageFromApi(nl);
          }
        }
      } else {
        Future<(String, String, String?)> getImageFromApi() async {
          // get image url through api
          Res<String>? apiRes = await EhNetwork().apiRequest({
            "gid": int.parse(gid),
            "imgkey": imgKey,
            "method": "showpage",
            "page": page,
            "showkey": gallery.auth!["showKey"]
          });

          if (apiRes.error && apiRes.errorMessage!.contains("handshake")) {
            throw "Failed to make api request.\n"
                "This may be due to too frequent requests.\n"
                "Try to wait for some time and retry.";
          }

          var apiJson = const JsonDecoder().convert(apiRes.data);

          var i6 = apiJson["i6"] as String;

          RegExp regex = RegExp(r"nl\('(.+?)'\)");
          var nl = regex.firstMatch(i6)?.group(1);

          var originImage = i6.split("<a href=\"").last.split("\">").first;

          var image = apiJson["i3"] as String;

          image = image.substring(
              image.indexOf("src=\"") + 5, image.indexOf("\" style"));

          return (image, originImage, nl);
        }

        Future<(String, String, String?)> getImageFromHtml() async {
          var res = await EhNetwork().request(readerLink);
          if (res.error) {
            throw res.errorMessage ?? "error";
          } else {
            var document = parse(res.data);
            var image =
                document.querySelector("div#i3 > a > img")?.attributes["src"];
            var nl = document
                .querySelector("div#i6 > div > a#loadfail")
                ?.attributes["onclick"]
                ?.split('\'')
                .firstWhereOrNull((element) => element.contains('-'));
            var originImage = document
                    .querySelectorAll("div#i6 > div > a")
                    .firstWhereOrNull(
                        (element) => element.text.contains("original"))
                    ?.attributes["href"] ??
                "";
            return (image ?? "", originImage, nl);
          }
        }

        String image, originImage;
        String? nl;

        try {
          (image, originImage, nl) = await getImageFromApi();
        } catch (e) {
          (image, originImage, nl) = await getImageFromHtml();
        }

        if (image.contains("509.gif")) {
          throw ImageExceedError();
        }

        if (appdata.settings[29] == "1" && originImage.isURL) {
          image = originImage;
        }

        int retryTimes = 0;
        var currentBytes = 0;

        while (true) {
          try {
            data.clear();
            cachingFile.reset();
            if (image == "") {
              throw "empty url";
            }
            res = await dio.get<ResponseBody>(image,
                options: Options(responseType: ResponseType.stream));
            if (res.data!.headers["Content-Type"]?[0] ==
                    "text/html; charset=UTF-8" ||
                res.data!.headers["content-type"]?[0] ==
                    "text/html; charset=UTF-8") {
              throw ImageExceedError();
            }
            var stream = res.data!.stream;
            int? expectedBytes;
            try {
              expectedBytes =
                  int.parse(res.data!.headers["Content-Length"]![0]);
            } catch (e) {
              try {
                expectedBytes =
                    int.parse(res.data!.headers["content-length"]![0]);
              } finally {}
            }

            await for (var b in stream) {
              await cachingFile.writeBytes(b);
              currentBytes += b.length;
              data.addAll(b);
              var progress = DownloadProgress(
                currentBytes,
                expectedBytes + 1,
                cacheKey,
                savePath,
              );
              yield progress;
              loadingItems[cacheKey] = progress;
            }
            totalBytes = currentBytes;
            break;
          } catch (e) {
            retryTimes++;
            if (retryTimes == 4) {
              throw "Failed to load image.\nMaximum number of retries reached.";
            }
            if (nl == null) {
              rethrow;
            }
            var (newImage, newNl) = await EhNetwork().getImageLinkWithNL(
                getGalleryId(galleryLink), imgKey, page, nl);
            image = newImage;
            if (kDebugMode) {
              print("Get new image: $image, new nl $newNl");
            }
            if (newNl != null) {
              nl = newNl;
            }
          }
        }
      }

      await cachingFile.close();
      var ext = detectFileType(data).ext.replaceFirst(('.'), '');
      CacheManager().setType(key, ext);
      yield DownloadProgress(
        totalBytes,
        totalBytes,
        cacheKey,
        savePath,
        Uint8List.fromList(data),
        ext,
      );
    } catch (e, s) {
      caching?.cancel();
      LogManager.addLog(LogLevel.error, "Network", "$e\n$s");
      if (e is DioException && e.type == DioExceptionType.badResponse) {
        var statusCode = e.response?.statusCode;
        if (statusCode != null && statusCode >= 400 && statusCode < 500) {
          throw BadRequestException(e.message.toString());
        }
      }
      rethrow;
    } finally {
      loadingItems.remove(cacheKey);
    }
  }

  ///为Hitomi设计的图片加载函数
  ///
  /// 使用hash标识图片
  Stream<DownloadProgress> getHitomiImage(
      HitomiFile image, String galleryId) async* {
    await wait(image.hash);
    loadingItems[image.hash] = DownloadProgress(0, 1, image.hash, "");
    CachingFile? caching;

    try {
      final key = image.hash;
      var cache = await CacheManager().findCache(key);
      if (cache != null) {
        yield DownloadProgress(
            1, 1, key, cache, null, CacheManager().getType(key));
        loadingItems.remove(key);
        return;
      }

      final cachingFile = await CacheManager().openWrite(key);
      caching = cachingFile;
      final savePath = cachingFile.file.path;
      yield DownloadProgress(0, 100, key, savePath);

      final gg = GG();
      var url = await gg.urlFromUrlFromHash(galleryId, image, 'webp', null);
      int l;
      for (l = url.length - 1; l >= 0; l--) {
        if (url[l] == '.') {
          break;
        }
      }
      var dio = logDio();
      dio.options.headers = {
        "User-Agent": webUA,
        "Referer": "https://hitomi.la/reader/$galleryId.html"
      };

      var res = await dio.get<ResponseBody>(url,
          options: Options(responseType: ResponseType.stream));
      var stream = res.data!.stream;
      int? expectedBytes;
      try {
        expectedBytes = int.parse(res.data!.headers["Content-Length"]![0]);
      } catch (e) {
        try {
          expectedBytes = int.parse(res.data!.headers["content-length"]![0]);
        } catch (e) {
          //忽视
        }
      }
      var currentBytes = 0;
      var data = <int>[];
      await for (var b in stream) {
        data.addAll(b);
        await cachingFile.writeBytes(b);
        currentBytes += b.length;
        var progress = DownloadProgress(
            currentBytes, (expectedBytes ?? currentBytes + 1), url, savePath);
        yield progress;
        loadingItems[image.hash] = progress;
      }
      await cachingFile.close();
      var ext = getExt(res);
      CacheManager().setType(key, ext);
      yield DownloadProgress(currentBytes, currentBytes, url, savePath,
          Uint8List.fromList(data), ext);
    } catch (e) {
      caching?.cancel();
      if (e is DioException && e.type == DioExceptionType.badResponse) {
        var statusCode = e.response?.statusCode;
        if (statusCode != null && statusCode >= 400 && statusCode < 500) {
          throw BadRequestException(e.message.toString());
        }
      }
      rethrow;
    } finally {
      loadingItems.remove(image.hash);
    }
  }

  ///获取禁漫图片, 如果缓存中没有, 则尝试下载
  Stream<DownloadProgress> getJmImage(String url, Map<String, String>? headers,
      {required String epsId,
      required String scrambleId,
      required String bookId}) async* {
    bookId = bookId.replaceAll(RegExp(r"\..+"), "");
    final urlWithoutParam = url.replaceAll(RegExp(r"\?.+"), "");
    await wait(urlWithoutParam);
    loadingItems[urlWithoutParam] = DownloadProgress(0, 1, url, "");
    CachingFile? caching;

    try {
      final key = urlWithoutParam;
      var cache = await CacheManager().findCache(key);
      if (cache != null) {
        yield DownloadProgress(1, 1, url, cache);
        loadingItems.remove(url);
        return;
      }

      final cachingFile = await CacheManager().openWrite(key);
      caching = cachingFile;
      final savePath = cachingFile.file.path;
      yield DownloadProgress(0, 1, url, savePath);

      var dio = logDio();

      var bytes = <int>[];
      String? ext;
      // On Web the gateway can recombine the JM scramble server-side (and
      // cache the result), which also lets the browser cache the final image.
      // Older gateways pass WebP through untouched; those responses still
      // need the client-side recombine below.
      var gatewayRecombined = false;
      try {
        Response<ResponseBody>? signedRes = await _getSignedImageResponse(
            url, const {},
            profile: "jm",
            jm: {
              "epsId": epsId,
              "scrambleId": scrambleId,
              "bookId": bookId,
            });
        if (signedRes != null) {
          final rawContentType = signedRes.headers["Content-Type"] ??
              signedRes.headers["content-type"];
          final contentType = rawContentType?.first.toLowerCase();
          gatewayRecombined = contentType != null &&
              !contentType.contains("webp");
        }
        final res = signedRes ??
            await dio.get<ResponseBody>(url,
                options: Options(
                    responseType: ResponseType.stream,
                    headers: getImgHeaders()));
        ext = getExt(res);
        var stream = res.data!.stream;
        await for (var b in stream) {
          //不直接写入文件, 因为需要对图片进行重组, 处理完成后再写入
          bytes.addAll(b);
          //构建虚假的进度条, 因为无法获取jm文件大小
          var total = max(1 << 20, bytes.length + 1);
          var progress = DownloadProgress(bytes.length, total, url, savePath);
          yield progress;
          loadingItems[urlWithoutParam] = progress;
        }
      } catch (e) {
        rethrow;
      }
      var progress = DownloadProgress(
        bytes.length,
        (bytes.length / 0.75).toInt(),
        url,
        savePath,
      );
      yield progress;
      loadingItems[urlWithoutParam] = progress;
      // JM currently serves WebP for nearly all pages and those files are
      // scrambled exactly like the older JPEGs: the gateway recombines both
      // formats (live-verified against chapter 1487055), so WebP must NOT be
      // skipped here. Only pages that did not come back already recombined
      // from a current gateway take this client-side pass.
      if (url.split('.').last != "gif" && !gatewayRecombined) {
        bytes = await startRecombineAndWriteImage(
            Uint8List.fromList(bytes), epsId, scrambleId, bookId, savePath);
      }
      await cachingFile.writeBytes(bytes);
      await cachingFile.close();
      CacheManager().setType(key, ext);
      progress = DownloadProgress(
        bytes.length,
        bytes.length,
        url,
        savePath,
        Uint8List.fromList(bytes),
        ext,
      );
      yield progress;
    } catch (e) {
      caching?.cancel();
      if (e is DioException && e.type == DioExceptionType.badResponse) {
        var statusCode = e.response?.statusCode;
        if (statusCode != null && statusCode >= 400 && statusCode < 500) {
          throw BadRequestException(e.message.toString());
        }
      }
      rethrow;
    } finally {
      loadingItems.remove(urlWithoutParam);
    }
  }

  Stream<DownloadProgress> getCustomImage(
      String url, String comicId, String epId, String sourceKey) async* {
    var cacheKey = "$sourceKey$comicId$epId$url";
    await wait(cacheKey);
    loadingItems[cacheKey] = DownloadProgress(0, 1, cacheKey, "");

    var cache = await CacheManager().findCache(cacheKey);
    if (cache != null) {
      yield DownloadProgress(1, 1, cacheKey, cache);
      loadingItems.remove(cacheKey);
      return;
    }

    CachingFile? caching;

    var source = ComicSource.find(sourceKey) ??
        (throw "Unknown Comic Source $sourceKey");

    try {
      Map<String, dynamic> config;

      if (source.getImageLoadingConfig == null) {
        config = {};
      } else {
        config = source.getImageLoadingConfig!(url, comicId, epId);
      }

      caching = await CacheManager().openWrite(cacheKey);
      final savePath = caching.file.path;

      var res = await dio.request<ResponseBody>(config['url'] ?? url,
          data: config['data'],
          options: Options(
              method: config['method'] ?? 'GET',
              headers: config['headers'] ?? {'user-agent': webUA},
              responseType: ResponseType.stream));

      List<int> imageData = [];

      int? expectedBytes = res.data!.contentLength;
      if (expectedBytes == -1) {
        expectedBytes = null;
      }

      bool shouldModifyData = config['onResponse'] != null;

      await for (var data in res.data!.stream) {
        if (!shouldModifyData) {
          await caching.writeBytes(data);
        }
        imageData.addAll(data);
        var progress = DownloadProgress(
          imageData.length,
          (expectedBytes ?? imageData.length + 1),
          url,
          savePath,
        );
        yield progress;
        loadingItems[cacheKey] = progress;
      }

      Uint8List? result;

      if (shouldModifyData) {
        var data = (config['onResponse']
            as JSInvokable)(Uint8List.fromList(imageData));
        imageData.clear();
        if (data is! Uint8List) {
          throw "Invalid Config: onImageLoad.onResponse return invalid type\n"
              "Expected: Uint8List(ArrayBuffer)\n"
              "Got: ${data.runtimeType}";
        }
        result = data;
        await caching.writeBytes(data);
      }

      var ext = getExt(res);
      CacheManager().setType(cacheKey, ext);
      await caching.close();
      var length = result?.length ?? imageData.length;
      yield DownloadProgress(
        length,
        length,
        url,
        savePath,
        result ?? Uint8List.fromList(imageData),
        ext,
      );
    } catch (e) {
      caching?.cancel();
      if (e is DioException && e.type == DioExceptionType.badResponse) {
        var statusCode = e.response?.statusCode;
        if (statusCode != null && statusCode >= 400 && statusCode < 500) {
          throw BadRequestException(e.message.toString());
        }
      }
      rethrow;
    } finally {
      loadingItems.remove(cacheKey);
    }
  }

  Future<void> wait(String cacheKey) {
    int timeout = 50;
    return Future.doWhile(() async {
      await Future.delayed(const Duration(milliseconds: 300));
      timeout--;
      if (timeout == 0) {
        loadingItems.remove(cacheKey);
        return false;
      }
      return loadingItems[cacheKey] != null;
    });
  }

  Stream<DownloadProgress> getCustomThumbnail(
      String url, String sourceKey, [Map<String, String>? headers]) async* {
    var cacheKey = "$sourceKey$url";
    await wait(cacheKey);
    loadingItems[cacheKey] = DownloadProgress(0, 1, cacheKey, "");

    var cache = await CacheManager().findCache(cacheKey);
    if (cache != null) {
      yield DownloadProgress(1, 1, cacheKey, cache);
      loadingItems.remove(cacheKey);
      return;
    }

    CachingFile? caching;

    var source = ComicSource.find(sourceKey) ??
        (throw "Unknown Comic Source $sourceKey");

    try {
      Map<String, dynamic> config;

      if (source.getThumbnailLoadingConfig == null) {
        config = {};
      } else {
        config = source.getThumbnailLoadingConfig!(url);
      }

      config['headers'] ??= headers;

      caching = await CacheManager().openWrite(cacheKey);
      final savePath = caching.file.path;

      var res = await dio.request<ResponseBody>(config['url'] ?? url,
          data: config['data'],
          options: Options(
              method: config['method'] ?? 'GET',
              headers: config['headers'] ?? {'user-agent': webUA},
              responseType: ResponseType.stream));

      List<int> imageData = [];

      int? expectedBytes = res.data!.contentLength;
      if (expectedBytes == -1) {
        expectedBytes = null;
      }

      bool shouldModifyData = config['onResponse'] != null;

      await for (var data in res.data!.stream) {
        if (!shouldModifyData) {
          await caching.writeBytes(data);
        }
        imageData.addAll(data);
        var progress = DownloadProgress(imageData.length,
            expectedBytes ?? (imageData.length + 1), url, savePath);
        yield progress;
        loadingItems[cacheKey] = progress;
      }

      Uint8List? result;

      if (shouldModifyData) {
        var data = (config['onResponse']
            as JSInvokable)(Uint8List.fromList(imageData));
        imageData.clear();
        if (data is! Uint8List) {
          throw "Invalid Config: onImageLoad.onResponse return invalid type\n"
              "Expected: Uint8List(ArrayBuffer)\n"
              "Got: ${data.runtimeType}";
        }
        result = data;
        await caching.writeBytes(data);
      }

      await caching.close();
      yield DownloadProgress(
          1, 1, url, savePath, result ?? Uint8List.fromList(imageData));
    } catch (e) {
      Log.error("Network", "Failed to load a image:\nUrl:$url\nError:$e");
      caching?.cancel();
      if (e is DioException && e.type == DioExceptionType.badResponse) {
        var statusCode = e.response?.statusCode;
        if (statusCode != null && statusCode >= 400 && statusCode < 500) {
          throw BadRequestException(e.message.toString());
        }
      }
      rethrow;
    } finally {
      loadingItems.remove(cacheKey);
    }
  }

  Future<File?> getFile(String key) async {
    var cache = await CacheManager().findCache(key);
    if (cache != null) {
      return File(cache);
    }
    return null;
  }

  Future<void> clear() async {
    await CacheManager().clear();
  }

  Future<bool> find(String key) async {
    return await CacheManager().findCache(key) != null;
  }

  Future<void> delete(String key) async {
    await CacheManager().delete(key);
  }

  String? getExt(Response res) {
    String? ext;
    var url = res.realUri.toString();
    var contentType =
        (res.headers["Content-Type"] ?? res.headers["content-type"])?[0];
    if (contentType != null) {
      ext = switch (contentType) {
        "image/jpeg" => "jpg",
        "image/png" => "png",
        "image/gif" => "gif",
        "image/webp" => "webp",
        _ => null
      };
    }
    ext ??= url.split('.').last;
    if (!["jpg", "jpeg", "png", "gif", "webp"].contains(ext)) {
      ext = "jpg";
      LogManager.addLog(
          LogLevel.warning,
          "ImageManager",
          "Unknown image extension: \n"
              "Content-Type: $contentType\n"
              "URL: $url");
    }
    return ext;
  }
}

class DownloadProgress {
  final int _currentBytes;
  final int _expectedBytes;
  final String url;
  final String savePath;
  final Uint8List? data;
  final String? ext;

  int get currentBytes => _currentBytes;

  int get expectedBytes => _expectedBytes;

  bool get finished => _currentBytes == _expectedBytes;

  const DownloadProgress(
      this._currentBytes, this._expectedBytes, this.url, this.savePath,
      [this.data, this.ext]);

  File getFile() => File(savePath);
}

class _SignedImage {
  final String path;
  final DateTime expiresAt;

  const _SignedImage(this.path, this.expiresAt);
}

class ImageExceedError extends Error {
  @override
  String toString() => "Maximum image loading limit reached.";
}
