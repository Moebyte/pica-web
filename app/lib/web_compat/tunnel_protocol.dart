/// Pure helpers for the gateway tunnel protocol.
///
/// These functions contain no JS interop so they can be unit-tested on the
/// Dart VM. They implement the client side of the `/api/tunnel` contract:
///
/// - `X-Pica-Req-Headers`: base64url (no padding) JSON object of the original
///   request headers.
/// - `X-Pica-Meta`: base64url (no padding) JSON object with
///   `{status, headers, setCookies, finalUrl}`.
library;

import 'dart:convert';

/// Same-origin gateway base. Empty means "same origin as the app"; set this to
/// an absolute URL (or a sub-path prefix) when the gateway lives elsewhere.
const String gatewayBase = '';

/// Tunnel endpoint path.
const String tunnelPath = '/api/tunnel';

/// Resolves the tunnel endpoint against the current page URI.
Uri resolveTunnelUri([Uri? pageUri]) {
  final base = pageUri ?? Uri.base;
  return base.resolve('$gatewayBase$tunnelPath');
}

bool isSameOrigin(Uri a, Uri b) {
  return a.scheme == b.scheme && a.host == b.host && a.port == b.port;
}

/// Whether [target] should be sent through the gateway tunnel.
///
/// Same-origin requests (`/api/*`, static assets, the app itself) and
/// non-http(s) URIs must not be tunneled to avoid self-loops.
bool shouldTunnel(Uri target, {required Uri gatewayUri}) {
  if (target.scheme != 'http' && target.scheme != 'https') {
    return false;
  }
  if (target.host.isEmpty) {
    return false;
  }
  return !isSameOrigin(target, gatewayUri);
}

/// Normalizes arbitrary Dio header values into strings.
///
/// - null values are skipped (Dio uses null to mean "not set");
/// - iterables are joined with `, ` (multiple header values);
/// - CR/LF are replaced to avoid header injection.
Map<String, String> normalizeHeaderMap(Map<String, dynamic> headers) {
  final result = <String, String>{};
  headers.forEach((key, value) {
    final name = key.trim();
    if (name.isEmpty || value == null) {
      return;
    }
    String text;
    if (value is String) {
      text = value;
    } else if (value is Iterable) {
      text = value
          .where((element) => element != null)
          .map((element) => element.toString())
          .join(', ');
    } else {
      text = value.toString();
    }
    if (text.isEmpty) {
      return;
    }
    result[name] = sanitizeHeaderValue(text);
  });
  return result;
}

String sanitizeHeaderValue(String value) {
  return value.replaceAll('\r', ' ').replaceAll('\n', ' ');
}

/// Encodes [value] as UTF-8 JSON in base64url without padding.
String encodeBase64UrlJson(Object? value) {
  final bytes = utf8.encode(jsonEncode(value));
  return base64Url.encode(bytes).replaceAll('=', '');
}

/// Encodes the original request headers for `X-Pica-Req-Headers`.
String encodeRequestHeaders(Map<String, dynamic> headers) {
  return encodeBase64UrlJson(normalizeHeaderMap(headers));
}

/// Decodes a base64url value, accepting both padded and unpadded input.
String? decodeBase64UrlText(String? value) {
  if (value == null || value.isEmpty) {
    return null;
  }
  try {
    final normalized = base64Url.normalize(value);
    return utf8.decode(base64Url.decode(normalized));
  } catch (_) {
    return null;
  }
}

/// Parsed `X-Pica-Meta` document.
class PicaTunnelMeta {
  const PicaTunnelMeta({
    required this.status,
    required this.headers,
    required this.setCookies,
    required this.finalUrl,
  });

  final int? status;
  final Map<String, List<String>> headers;
  final List<String> setCookies;
  final String? finalUrl;

  factory PicaTunnelMeta.fromJson(Map<String, dynamic> json) {
    final headers = <String, List<String>>{};
    final rawHeaders = json['headers'];
    if (rawHeaders is Map) {
      rawHeaders.forEach((key, value) {
        if (key is! String) {
          return;
        }
        final name = key.trim();
        if (name.isEmpty) {
          return;
        }
        final values = _toStringList(value);
        if (values.isNotEmpty) {
          headers[name] = values;
        }
      });
    }
    final rawCookies = json['setCookies'];
    final setCookies = rawCookies is Iterable
        ? rawCookies
            .where((element) => element != null)
            .map((element) => element.toString())
            .toList()
        : <String>[];
    final rawStatus = json['status'];
    final status = rawStatus is int
        ? rawStatus
        : int.tryParse(rawStatus?.toString() ?? '');
    final finalUrl = json['finalUrl'] is String ? json['finalUrl'] as String : null;
    return PicaTunnelMeta(
      status: status,
      headers: headers,
      setCookies: setCookies,
      finalUrl: finalUrl,
    );
  }
}

List<String> _toStringList(Object? value) {
  if (value == null) {
    return const [];
  }
  if (value is String) {
    return [value];
  }
  if (value is Iterable) {
    return value
        .where((element) => element != null)
        .map((element) => element.toString())
        .toList();
  }
  return [value.toString()];
}

/// Decodes `X-Pica-Meta`. Returns null when absent or malformed.
PicaTunnelMeta? decodeTunnelMeta(String? value) {
  final text = decodeBase64UrlText(value);
  if (text == null) {
    return null;
  }
  try {
    final json = jsonDecode(text);
    if (json is! Map<String, dynamic>) {
      return null;
    }
    return PicaTunnelMeta.fromJson(json);
  } catch (_) {
    return null;
  }
}

/// Builds the headers passed to `ResponseBody`.
///
/// Header names are lowercased because `ResponseBody.contentLength` looks up
/// `content-length` in the raw map, and Dio's response headers are
/// case-insensitive anyway. `setCookies` from the meta document is appended as
/// `set-cookie` so `CookieManagerSql.onResponse` keeps updating the local jar.
Map<String, List<String>> buildResponseHeaders(PicaTunnelMeta? meta) {
  final result = <String, List<String>>{};
  if (meta == null) {
    return result;
  }
  meta.headers.forEach((key, values) {
    final name = key.trim().toLowerCase();
    if (name.isEmpty || values.isEmpty) {
      return;
    }
    result.putIfAbsent(name, () => []).addAll(values);
  });
  if (meta.setCookies.isNotEmpty) {
    result.putIfAbsent('set-cookie', () => []).addAll(meta.setCookies);
  }
  return result;
}

/// Human-readable message for a gateway-generated error.
String gatewayErrorMessage(int status, String? code, String? body) {
  if (status == 401) {
    return 'Gateway authentication required (401). '
        'Log in to the gateway, or use gateway dev mode / nginx basic auth.';
  }
  final reason = code == null || code.isEmpty ? 'gateway_error' : code;
  final trimmed = body?.trim() ?? '';
  if (trimmed.isEmpty) {
    return 'Gateway error $status ($reason).';
  }
  final snippet = trimmed.length > 300 ? '${trimmed.substring(0, 300)}...' : trimmed;
  return 'Gateway error $status ($reason): $snippet';
}
