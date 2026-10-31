import 'dart:io';

import 'package:dio/dio.dart';
import 'package:pica_comic/foundation/log.dart';
import 'package:pica_comic/tools/extensions.dart';
import 'package:pica_comic/web_compat/sqlite.dart';

class CookieJarSql {
  late Database _db;

  final String path;

  CookieJarSql(this.path){
    init();
  }

  void init() {
    _db = openDb(path);
    _db.execute('''
      CREATE TABLE IF NOT EXISTS cookies (
        name TEXT NOT NULL,
        value TEXT NOT NULL,
        domain TEXT NOT NULL,
        path TEXT,
        expires INTEGER,
        secure INTEGER,
        httpOnly INTEGER,
        PRIMARY KEY (name, domain, path)
      );
    ''');
  }

  /// 导出全部 Cookie 行，供 GatewaySync 做账号态同步。
  List<Map<String, dynamic>> exportRows() {
    var rows = _db.select(
        'SELECT name, value, domain, path, expires, secure, httpOnly FROM cookies');
    return rows
        .map((row) => {
              'name': row['name'],
              'value': row['value'],
              'domain': row['domain'],
              'path': row['path'],
              'expires': row['expires'],
              'secure': row['secure'],
              'httpOnly': row['httpOnly'],
            })
        .toList();
  }

  /// 导入另一设备同步来的 Cookie 行（同键覆盖）。
  void importRows(List<dynamic> rows) {
    for (var row in rows) {
      if (row is! Map) continue;
      final name = row['name'];
      final value = row['value'];
      if (name is! String || value is! String) continue;
      _db.execute('''
        INSERT OR REPLACE INTO cookies (name, value, domain, path, expires, secure, httpOnly)
        VALUES (?, ?, ?, ?, ?, ?, ?);
      ''', [
        name,
        value,
        row['domain'] is String ? row['domain'] : '',
        row['path'] is String ? row['path'] : '/',
        row['expires'],
        (row['secure'] == 1 || row['secure'] == true) ? 1 : 0,
        (row['httpOnly'] == 1 || row['httpOnly'] == true) ? 1 : 0,
      ]);
    }
  }

  void saveFromResponse(Uri uri, List<Cookie> cookies) {
    var current = loadForRequest(uri);
    for (var cookie in cookies) {
      var currentCookie = current.firstWhereOrNull((element) =>
          element.name == cookie.name &&
          (cookie.path == null || cookie.path!.startsWith(element.path!)));
      if (currentCookie != null) {
        cookie.domain = currentCookie.domain;
      }
      _db.execute('''
        INSERT OR REPLACE INTO cookies (name, value, domain, path, expires, secure, httpOnly)
        VALUES (?, ?, ?, ?, ?, ?, ?);
      ''', [
        cookie.name,
        cookie.value,
        cookie.domain ?? uri.host,
        cookie.path ?? "/",
        cookie.expires?.millisecondsSinceEpoch,
        cookie.secure ? 1 : 0,
        cookie.httpOnly ? 1 : 0
      ]);
    }
  }

  List<Cookie> _loadWithDomain(String domain) {
    var rows = _db.select('''
      SELECT name, value, domain, path, expires, secure, httpOnly
      FROM cookies
      WHERE domain = ?;
    ''', [domain]);

    return rows
        .map((row) => Cookie(
              row["name"] as String,
              row["value"] as String,
            )
              ..domain = row["domain"] as String
              ..path = row["path"] as String
              ..expires = row["expires"] == null
                  ? null
                  : DateTime.fromMillisecondsSinceEpoch(row["expires"] as int)
              ..secure = row["secure"] == 1
              ..httpOnly = row["httpOnly"] == 1)
        .toList();
  }

  List<String> _getAcceptedDomains(String host) {
    var acceptedDomains = <String>[host];
    var hostParts = host.split(".");
    for (var i = 0; i < hostParts.length - 1; i++) {
      acceptedDomains.add(".${hostParts.sublist(i).join(".")}");
    }
    return acceptedDomains;
  }

  List<Cookie> loadForRequest(Uri uri) {
    // if uri.host is example.example.com, acceptedDomains will be [".example.example.com", ".example.com", "example.com"]
    var acceptedDomains = _getAcceptedDomains(uri.host);

    var cookies = <Cookie>[];
    for (var domain in acceptedDomains) {
      cookies.addAll(_loadWithDomain(domain));
    }

    // check expires
    var expires = cookies.where((cookie) =>
        cookie.expires != null && cookie.expires!.isBefore(DateTime.now()));
    for (var cookie in expires) {
      _db.execute('''
        DELETE FROM cookies
        WHERE name = ? AND domain = ? AND path = ?;
      ''', [cookie.name, cookie.domain, cookie.path]);
    }

    return cookies
        .where((element) =>
            !expires.contains(element) && _checkPathMatch(uri, element.path))
        .toList();
  }

  bool _checkPathMatch(Uri uri, String? cookiePath) {
    if (cookiePath == null) {
      return true;
    }

    if (cookiePath == uri.path) {
      return true;
    }

    if (cookiePath == "/") {
      return true;
    }

    if (cookiePath.endsWith("/")) {
      return uri.path.startsWith(cookiePath);
    }

    return uri.path.startsWith(cookiePath);
  }

  void saveFromResponseCookieHeader(Uri uri, List<String> cookieHeader) {
    var cookies = <Cookie>[];
    for (var header in cookieHeader) {
      try {
        cookies.add(Cookie.fromSetCookieValue(header));
      } catch (e) {
        // A single malformed Set-Cookie must not fail the whole response.
        LogManager.addLog(LogLevel.warning, "Cookie",
            "Ignoring invalid Set-Cookie header: $header\n$e");
      }
    }
    if (cookies.isNotEmpty) {
      saveFromResponse(uri, cookies);
    }
  }

  String loadForRequestCookieHeader(Uri uri) {
    var cookies = loadForRequest(uri);
    var map = <String, Cookie>{};
    for (var cookie in cookies) {
      var domain = cookie.domain ?? '';
      var existing = map[cookie.name];
      if (existing == null) {
        map[cookie.name] = cookie;
        continue;
      }
      var existingDomain = existing.domain ?? '';
      if (domain.isNotEmpty &&
          !domain.startsWith('.') &&
          existingDomain.startsWith('.')) {
        map[cookie.name] = cookie;
      } else if (domain.length > existingDomain.length) {
        map[cookie.name] = cookie;
      }
    }
    return map.entries.map((cookie) => "${cookie.value.name}=${cookie.value.value}").join("; ");
  }

  void delete(Uri uri, String name) {
    var acceptedDomains = _getAcceptedDomains(uri.host);
    for (var domain in acceptedDomains) {
      _db.execute('''
        DELETE FROM cookies
        WHERE name = ? AND domain = ? AND path = ?;
      ''', [name, domain, uri.path]);
    }
  }

  void deleteUri(Uri uri) {
    var acceptedDomains = _getAcceptedDomains(uri.host);
    for (var domain in acceptedDomains) {
      _db.execute('''
        DELETE FROM cookies
        WHERE domain = ?;
      ''', [domain]);
    }
  }

  void deleteAll() {
    _db.execute('''
      DELETE FROM cookies;
    ''');
  }

  void dispose() {
    _db.dispose();
  }
}

class SingleInstanceCookieJar extends CookieJarSql {
  factory SingleInstanceCookieJar(String path) =>
      instance ??= SingleInstanceCookieJar._create(path);

  SingleInstanceCookieJar._create(super.path);

  static SingleInstanceCookieJar? instance;
}

class CookieManagerSql extends Interceptor {
  final CookieJarSql cookieJar;

  CookieManagerSql(this.cookieJar);

  @override
  void onRequest(RequestOptions options, RequestInterceptorHandler handler) {
    var cookies = cookieJar.loadForRequestCookieHeader(options.uri);
    if (cookies.isNotEmpty) {
      options.headers["cookie"] = cookies;
    }
    handler.next(options);
  }

  @override
  void onResponse(Response response, ResponseInterceptorHandler handler) {
    cookieJar.saveFromResponseCookieHeader(
        response.requestOptions.uri, response.headers["set-cookie"] ?? []);
    handler.next(response);
  }

  @override
  void onError(DioException err, ErrorInterceptorHandler handler) {
    handler.next(err);
  }
}
