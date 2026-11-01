/// Web Dio adapter: routes cross-origin requests through the pica-gateway
/// tunnel (`POST /api/tunnel`).
///
/// Same-origin requests (`/api/*`, static assets, the app itself) are handed to
/// [BrowserHttpClientAdapter] so they never loop back into the tunnel.
///
/// The tunnel protocol itself is implemented in `tunnel_protocol.dart` and is
/// unit tested on the VM; this file only contains the JS interop plumbing.
library;

import 'dart:async';
import 'dart:convert';
import 'dart:js_interop';
import 'dart:typed_data';

import 'package:dio/browser.dart';
import 'package:dio/dio.dart';
import 'package:web/web.dart' as web;

import 'tunnel_protocol.dart';

/// Dio adapter that talks to the gateway tunnel.
class TunnelAdapter implements HttpClientAdapter {
  TunnelAdapter({Uri? pageUri}) : _pageUri = pageUri;

  final Uri? _pageUri;

  final BrowserHttpClientAdapter _browser = BrowserHttpClientAdapter();

  Uri get _tunnelUri => resolveTunnelUri(_pageUri);

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final target = options.uri;
    if (!shouldTunnel(target, gatewayUri: _tunnelUri)) {
      // 同源请求直接走浏览器栈。浏览器禁止脚本设置 User-Agent / Referer /
      // Cookie / Origin 等头，上游代码若透传了源站头会刷控制台警告，
      // 这里最后再兜一层，统一剔除。
      return _browser.fetch(
          _withoutBrowserForbiddenHeaders(options), requestStream, cancelFuture);
    }

    final controller = web.AbortController();
    var cancelled = false;
    cancelFuture?.then((_) {
      cancelled = true;
      controller.abort();
    });

    try {
      final body = await _collectBody(options.method, requestStream);
      final requestHeaders = web.Headers()
        ..set('X-Target-URL', target.toString())
        ..set('X-Target-Method', options.method)
        ..set('X-Pica-Follow', options.followRedirects ? '1' : '0')
        ..set('X-Pica-Req-Headers', encodeRequestHeaders(options.headers));

      final init = web.RequestInit(
        method: 'POST',
        headers: requestHeaders,
        body: body.isEmpty ? null : body.toJS,
        credentials: 'include',
        signal: controller.signal,
      );

      final response = await web.window
          .fetch(_tunnelUri.toString().toJS, init)
          .toDart;

      final meta = decodeTunnelMeta(response.headers.get('X-Pica-Meta'));
      final errorCode = response.headers.get('X-Pica-Error');
      final status = meta?.status ?? response.status;
      final responseHeaders = buildResponseHeaders(meta);

      if (errorCode != null) {
        // 网关自身的错误响应很小，整包读出用于错误文案。
        final bytes = await _readBody(response);
        final text = _tryDecodeText(bytes);
        final contentType = response.headers.get('Content-Type');
        if (contentType != null) {
          responseHeaders['content-type'] = [contentType];
        }
        throw DioException(
          requestOptions: options,
          response: Response(
            requestOptions: options,
            statusCode: status,
            statusMessage: response.statusText,
            headers: Headers.fromMap(responseHeaders),
            data: text,
          ),
          type: DioExceptionType.badResponse,
          message: gatewayErrorMessage(status, errorCode, text),
        );
      }

      // 流式返回：不再整包 arrayBuffer 缓冲，大图/大响应边到边解码，
      // 浏览器内存占用随窗口滚动而释放。HEAD/204/304 无 body。
      final bodyAllowed = options.method != 'HEAD' &&
          status != 204 &&
          status != 304;
      if (!bodyAllowed && !responseHeaders.containsKey('content-length')) {
        responseHeaders['content-length'] = ['0'];
      }

      return ResponseBody(
        bodyAllowed ? _readBodyStream(response) : const Stream<Uint8List>.empty(),
        status,
        statusMessage: response.statusText,
        headers: responseHeaders,
        isRedirect: status >= 300 && status < 400,
      );
    } on DioException {
      rethrow;
    } catch (error) {
      if (cancelled) {
        throw DioException(
          requestOptions: options,
          type: DioExceptionType.cancel,
          message: 'Request cancelled',
          error: error,
        );
      }
      throw DioException(
        requestOptions: options,
        type: DioExceptionType.unknown,
        error: error,
        message: 'Tunnel request failed: $error',
      );
    }
  }

  static const _browserForbiddenHeaders = {
    'user-agent',
    'referer',
    'cookie',
    'origin',
    'host',
    'content-length',
    'accept-encoding',
  };

  static RequestOptions _withoutBrowserForbiddenHeaders(RequestOptions options) {
    final hasForbidden = options.headers.keys
        .any((k) => _browserForbiddenHeaders.contains(k.toLowerCase()));
    if (!hasForbidden) {
      return options;
    }
    final headers = Map<String, dynamic>.from(options.headers);
    headers.removeWhere(
        (k, _) => _browserForbiddenHeaders.contains(k.toLowerCase()));
    return options.copyWith(headers: headers);
  }

  Future<Uint8List> _collectBody(
      String method, Stream<Uint8List>? requestStream) async {
    if (method == 'GET' || method == 'HEAD' || requestStream == null) {
      return Uint8List(0);
    }
    final builder = BytesBuilder(copy: false);
    await for (final chunk in requestStream) {
      builder.add(chunk);
    }
    return builder.takeBytes();
  }

  Future<Uint8List> _readBody(web.Response response) async {
    final buffer = await response.arrayBuffer().toDart;
    return buffer.toDart.asUint8List();
  }

  /// 流式读取隧道响应体：逐块产出，避免把整张图/整个响应缓冲在 JS 堆里。
  Stream<Uint8List> _readBodyStream(web.Response response) async* {
    final body = response.body;
    if (body == null) {
      return;
    }
    final reader = body.getReader() as web.ReadableStreamDefaultReader;
    try {
      while (true) {
        final result = await reader.read().toDart;
        if (result.done) {
          break;
        }
        final chunk = _chunkToBytes(result.value);
        if (chunk != null && chunk.isNotEmpty) {
          yield chunk;
        }
      }
    } finally {
      try {
        reader.cancel().toDart.ignore();
      } catch (_) {
        // 流已经关闭/取消，忽略。
      }
    }
  }

  static Uint8List? _chunkToBytes(JSAny? value) {
    if (value == null) {
      return null;
    }
    if (value is JSUint8Array) {
      return value.toDart;
    }
    if (value is JSArrayBuffer) {
      return value.toDart.asUint8List();
    }
    return null;
  }

  String _tryDecodeText(Uint8List bytes) {
    if (bytes.isEmpty) {
      return '';
    }
    try {
      return utf8.decode(bytes);
    } catch (_) {
      return '';
    }
  }

  @override
  void close({bool force = false}) {
    _browser.close(force: force);
  }
}

HttpClientAdapter createPlatformHttpAdapter({
  required bool http2,
  String? proxy,
}) {
  return TunnelAdapter();
}

HttpClientAdapter createPlatformHttpAdapterSync({
  required bool http2,
  String? proxy,
}) {
  return TunnelAdapter();
}
