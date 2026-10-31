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
      return _browser.fetch(options, requestStream, cancelFuture);
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
      final bytes = await _readBody(response);
      final status = meta?.status ?? response.status;
      final responseHeaders = buildResponseHeaders(meta);

      if (errorCode != null) {
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

      // Body is read fully (MVP); report its real length for progress
      // reporting. For HEAD/204/304 the upstream Content-Length is preserved
      // when present, otherwise it becomes 0.
      final bodyAllowed = options.method != 'HEAD' &&
          status != 204 &&
          status != 304;
      if (bodyAllowed || !responseHeaders.containsKey('content-length')) {
        responseHeaders['content-length'] = [bytes.length.toString()];
      }

      return ResponseBody(
        Stream<Uint8List>.value(bytes),
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
