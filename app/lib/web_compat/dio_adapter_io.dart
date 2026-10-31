/// Native Dio adapter factory (dart:io based).
library;

import 'package:dio/dio.dart';
import 'package:dio/io.dart';
import 'package:dio_http2_adapter/dio_http2_adapter.dart';

HttpClientAdapter createPlatformHttpAdapter({
  required bool http2,
  String? proxy,
}) {
  return createPlatformHttpAdapterSync(http2: http2, proxy: proxy);
}

HttpClientAdapter createPlatformHttpAdapterSync({
  required bool http2,
  String? proxy,
}) {
  if (!http2) {
    return IOHttpClientAdapter();
  }
  return Http2Adapter(ConnectionManager(
    idleTimeout: const Duration(seconds: 15),
    onClientCreate: (_, config) {
      if (proxy != null) {
        config.proxy = Uri.parse('http://$proxy');
      }
    },
  ));
}
