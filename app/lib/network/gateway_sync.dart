import 'dart:async';
import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';
import 'package:pica_comic/base.dart';
import 'package:pica_comic/foundation/history.dart';
import 'package:pica_comic/foundation/local_favorites.dart';
import 'package:pica_comic/foundation/log.dart';
import 'package:pica_comic/foundation/sync_hook.dart';
import 'package:pica_comic/network/app_dio.dart';
import 'package:pica_comic/network/cookie_jar.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// 把账号态与数据（收藏/历史/图片收藏）同步到网关加密存储
/// （`/api/store/{account,history,favorites}`），任意设备打开网页即
/// 处于已登录状态并共享数据。
///
/// 同步键：
/// - `account`：各源凭据（`data` 含 account/token）、CookieJar 全量导出、
///   implicitData 与账号相关设置
/// - `history`：history + image_favorites 全部行
/// - `favorites`：本地收藏夹全部表 + folder_order
///
/// 一致性策略：每个键带 `updated` 时间戳，本地记录已同步版本
/// （SharedPreferences `gsync.rev.<key>`）。服务端较新 → 整库替换（删除
/// 可传播）；首次双端都有数据 → 并集合并后回推；变化即去抖推送（2s），
/// 另有 60s 兜底推送。仅 Web 生效；原生仍走本地/WebDAV。
class GatewaySync {
  GatewaySync._() {
    accountSyncHook = schedulePush;
    if (kIsWeb) {
      // 兜底：钩子未覆盖到的写路径，最多延迟一分钟同步出去
      Timer.periodic(const Duration(seconds: 60), (_) => schedulePush());
    }
  }

  static final GatewaySync instance = GatewaySync._();

  static const _accountKey = 'account';
  static const _dataKeys = ['history', 'favorites'];
  static const _settingIndices = [15, 17, 20, 37, 69, 85, 88];
  static const _sourceDataPrefix = 'comic_source_data_';

  Timer? _timer;
  bool _pushing = false;

  final Completer<void> _accountRestoreDone = Completer<void>();
  final Completer<void> _dataRestoreDone = Completer<void>();

  /// 账号与收藏/历史都完成恢复（供启动时按需等待）
  Future<void> get restoreAll =>
      Future.wait([_accountRestoreDone.future, _dataRestoreDone.future]);

  Future<void> get accountRestoreDone => _accountRestoreDone.future;

  bool get enabled => kIsWeb;

  /// 服务端确实存在账号数据（说明这台机器以前已经完成过引导/登录）。
  bool _hasRemoteAccount = false;

  bool get hasRemoteAccount => _hasRemoteAccount;

  /// 启动时没能确认服务端状态（超时或请求失败）。Web 上把它当作“老用户”
  /// 处理，避免慢网络下新设备误进引导页。
  bool accountRestoreUncertain = false;

  String _endpoint(String key) =>
      Uri.base.resolve('/api/store/$key').toString();

  Dio? _dio;

  Dio get _client => _dio ??= createPicaDio(BaseOptions(
        connectTimeout: const Duration(seconds: 5),
        receiveTimeout: const Duration(seconds: 15),
        // 404 表示服务端还没有该键的数据，是正常分支
        validateStatus: (status) => status != null && status < 500,
      ));

  /// 启动时拉取账号态（在 ComicSource.init / 各网络层 init 之前调用）。
  Future<void> restore() async {
    if (!enabled) {
      if (!_accountRestoreDone.isCompleted) _accountRestoreDone.complete();
      return;
    }
    try {
      var res = await _client.get(_endpoint(_accountKey));
      if (res.statusCode == 200) {
        var value = res.data is Map ? res.data['value'] : null;
        if (value is String && value.isNotEmpty) {
          var blob = jsonDecode(value);
          if (blob is Map<String, dynamic>) {
            _hasRemoteAccount = true;
            await applyBlob(blob);
            log(
                "Restored account state from gateway "
                    "(${(blob['sources'] as Map?)?.length ?? 0} sources, "
                    "${(blob['cookies'] as List?)?.length ?? 0} cookies)",
                "GatewaySync");
          }
        }
      }
    } catch (e) {
      accountRestoreUncertain = true;
      log("restore failed: $e", "GatewaySync", LogLevel.warning);
    } finally {
      if (!_accountRestoreDone.isCompleted) _accountRestoreDone.complete();
    }
  }

  /// 启动时拉取数据键（在两个 manager init 之后调用——它们负责建表）。
  Future<void> restoreData() async {
    if (!enabled) {
      if (!_dataRestoreDone.isCompleted) _dataRestoreDone.complete();
      return;
    }
    for (final key in _dataKeys) {
      try {
        var res = await _client.get(_endpoint(key));
        if (res.statusCode != 200) continue;
        var value = res.data is Map ? res.data['value'] : null;
        if (value is! String || value.isEmpty) continue;
        var envelope = jsonDecode(value);
        if (envelope is! Map<String, dynamic>) continue;
        var updated = envelope['updated'];
        var data = envelope['data'];
        if (updated is! int || data is! Map<String, dynamic>) continue;

        final prefs = await SharedPreferences.getInstance();
        final localRev = prefs.getInt('gsync.rev.$key');
        if (localRev == updated) continue; // 已是同一版本

        if (localRev == null) {
          final localEmpty = _isEmpty(key);
          _import(key, data, replace: localEmpty);
          await prefs.setInt('gsync.rev.$key', updated);
          if (!localEmpty) {
            // 首次双端都有数据：并集合并后把合并结果推回服务端
            schedulePush();
          }
          log("Restored $key (first sync, merge=${!localEmpty})",
              "GatewaySync");
        } else {
          _import(key, data, replace: true);
          await prefs.setInt('gsync.rev.$key', updated);
          log("Restored $key (server newer: $localRev -> $updated)",
              "GatewaySync");
        }
      } catch (e) {
        log("restore $key failed: $e", "GatewaySync", LogLevel.warning);
      }
    }
    if (!_dataRestoreDone.isCompleted) _dataRestoreDone.complete();
  }

  /// 去抖推送：登录/登出/收藏/历史变化都会在 2 秒窗口内合并成一次推送。
  void schedulePush() {
    if (!enabled) return;
    _timer?.cancel();
    _timer = Timer(const Duration(seconds: 2), () {
      push();
    });
  }

  Future<void> push() async {
    if (!enabled || _pushing) return;
    _pushing = true;
    try {
      await _put(_accountKey, await buildBlob());
      for (final key in _dataKeys) {
        await _put(key, {
          'updated': DateTime.now().millisecondsSinceEpoch ~/ 1000,
          'data': _export(key),
        });
      }
    } catch (e) {
      log("push error: $e", "GatewaySync", LogLevel.warning);
    } finally {
      _pushing = false;
    }
  }

  Future<void> _put(String key, Object blob) async {
    try {
      var res = await _client.put(_endpoint(key), data: jsonEncode(blob));
      if (res.statusCode == 200) {
        final prefs = await SharedPreferences.getInstance();
        if (blob is Map && blob['updated'] is int) {
          await prefs.setInt('gsync.rev.$key', blob['updated'] as int);
        }
      } else {
        log("push $key failed: ${res.statusCode}", "GatewaySync",
            LogLevel.warning);
      }
    } catch (e) {
      log("push $key error: $e", "GatewaySync", LogLevel.warning);
    }
  }

  Map<String, dynamic> _export(String key) => switch (key) {
        'history' => HistoryManager().exportRows(),
        'favorites' => LocalFavoritesManager().exportRows(),
        _ => const {},
      };

  bool _isEmpty(String key) => switch (key) {
        'history' => HistoryManager().isEmptyForSync,
        'favorites' => LocalFavoritesManager().isEmptyForSync,
        _ => true,
      };

  void _import(String key, Map<String, dynamic> data, {required bool replace}) {
    switch (key) {
      case 'history':
        HistoryManager().importRows(data, replace: replace);
      case 'favorites':
        LocalFavoritesManager().importRows(data, replace: replace);
    }
  }

  /// 收集当前账号态。源数据直接读 SharedPreferences（键
  /// `comic_source_data_<key>`），不依赖 `ComicSource.sources` 是否已
  /// 初始化——启动早期的推送也不会把服务端账号误清空。
  Future<Map<String, dynamic>> buildBlob() async {
    final settings = <String, String>{};
    for (final i in _settingIndices) {
      if (i < appdata.settings.length) settings['$i'] = appdata.settings[i];
    }
    final sources = <String, dynamic>{};
    try {
      final s = await SharedPreferences.getInstance();
      for (var key in s.getKeys()) {
        if (!key.startsWith(_sourceDataPrefix)) continue;
        final raw = s.getString(key);
        if (raw == null || raw.isEmpty) continue;
        try {
          final data = jsonDecode(raw);
          if (data is Map && data.isNotEmpty) {
            sources[key.substring(_sourceDataPrefix.length)] = data;
          }
        } catch (_) {
          // 脏数据跳过
        }
      }
    } catch (e) {
      log("read source data failed: $e", "GatewaySync", LogLevel.warning);
    }
    final jar = SingleInstanceCookieJar.instance;
    return {
      'v': 1,
      'updated': DateTime.now().millisecondsSinceEpoch ~/ 1000,
      'implicitData': List<String>.from(appdata.implicitData),
      'firstUse': List<String>.from(appdata.firstUse),
      'settings': settings,
      'sources': sources,
      if (jar != null) 'cookies': jar.exportRows(),
    };
  }

  /// 把服务端 blob 套用到本地：内存 + 持久化存储都写，随后的
  /// `ComicSource.init()` 与各网络层 `init()`（reLogin）会接手生效。
  Future<void> applyBlob(Map<String, dynamic> blob) async {
    final implicit = blob['implicitData'];
    if (implicit is List && implicit.isNotEmpty) {
      for (var i = 0;
          i < implicit.length && i < appdata.implicitData.length;
          i++) {
        appdata.implicitData[i] = implicit[i].toString();
      }
      appdata.writeImplicitData();
    }

    final settings = blob['settings'];
    if (settings is Map && settings.isNotEmpty) {
      settings.forEach((key, value) {
        final i = int.tryParse(key.toString());
        if (i != null && i < appdata.settings.length && value is String) {
          appdata.settings[i] = value;
        }
      });
      await appdata.updateSettings(false);
    }

    final firstUse = blob['firstUse'];
    if (firstUse is List && firstUse.isNotEmpty) {
      for (var i = 0; i < firstUse.length && i < appdata.firstUse.length; i++) {
        // 只把已完成的标记合并进来，避免旧备份把本地的引导状态改回未完成。
        if (firstUse[i].toString() == "1") {
          appdata.firstUse[i] = "1";
        }
      }
      final prefs = await SharedPreferences.getInstance();
      await prefs.setStringList("firstUse", appdata.firstUse);
    }

    final sources = blob['sources'];
    if (sources is Map && sources.isNotEmpty) {
      final s = await SharedPreferences.getInstance();
      for (final entry in sources.entries) {
        final storageKey = '$_sourceDataPrefix${entry.key}';
        Map<String, dynamic> stored = {};
        final raw = s.getString(storageKey);
        if (raw != null && raw.isNotEmpty) {
          try {
            final decoded = jsonDecode(raw);
            if (decoded is Map) stored = Map<String, dynamic>.from(decoded);
          } catch (_) {}
        }
        final incoming = entry.value is Map
            ? Map<String, dynamic>.from(entry.value as Map)
            : <String, dynamic>{};
        await s.setString(
            storageKey, jsonEncode(mergeSourceData(stored, incoming)));
      }
    }

    final cookies = blob['cookies'];
    if (cookies is List && cookies.isNotEmpty) {
      SingleInstanceCookieJar.instance?.importRows(cookies);
    }
  }

  /// 纯函数：服务端源数据覆盖本地同名字段（`account: null` 表示对端已
  /// 登出，本地跟随登出）。
  static Map<String, dynamic> mergeSourceData(
      Map<String, dynamic> stored, Map<String, dynamic> incoming) {
    final out = Map<String, dynamic>.from(stored);
    incoming.forEach((key, value) {
      out[key] = value;
    });
    return out;
  }
}
