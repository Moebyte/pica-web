import 'dart:io' as io;

import 'package:app_links/app_links.dart';
import 'package:flutter/foundation.dart';
import 'package:cookie_jar/cookie_jar.dart';
import 'package:path_provider/path_provider.dart';
import 'package:pica_comic/comic_source/comic_source.dart';
import 'package:pica_comic/foundation/cache_manager.dart';
import 'package:pica_comic/foundation/history.dart';
import 'package:pica_comic/foundation/js_engine.dart';
import 'package:pica_comic/foundation/local_favorites.dart';
import 'package:pica_comic/foundation/log.dart';
import 'package:pica_comic/network/cookie_jar.dart';
import 'package:pica_comic/network/gateway_sync.dart';
import 'package:pica_comic/network/http_proxy.dart';
import 'package:pica_comic/network/jm_network/jm_network.dart';
import 'package:pica_comic/network/picacg_network/models.dart';
import 'package:pica_comic/tools/app_links.dart';
import 'package:pica_comic/tools/background_service.dart';
import 'package:pica_comic/tools/cache_auto_clear.dart';
import 'package:pica_comic/tools/io_extensions.dart';
import 'package:pica_comic/tools/io_tools.dart';
import 'package:pica_comic/tools/translations.dart';
import 'package:pica_comic/web_compat/sqlite.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:workmanager/workmanager.dart';

import 'base.dart';
import 'comic_source/built_in/ehentai.dart';
import 'comic_source/built_in/ht_manga.dart';
import 'comic_source/built_in/jm.dart';
import 'comic_source/built_in/nhentai.dart';
import 'comic_source/built_in/picacg.dart';
import 'foundation/app.dart';
import 'network/nhentai_network/nhentai_main_network.dart';

Future<void> init() async {
  try {
    await App.init();
    // Load sqlite3.wasm and register the IndexedDB VFS before any database is
    // opened (no-op on native platforms).
    await initWebDatabase();
    io.File? logFile = kIsWeb ? null : io.File("${App.dataPath}/log.txt");
    if(!kIsWeb && App.isAndroid) {
      var externalDirectory = await getExternalStorageDirectory();
      if (externalDirectory != null) {
        logFile = io.File("${externalDirectory.path}/log.txt");
      }
    }
    if(!kIsWeb && App.isIOS) {
      logFile = null;
    }
    if(logFile?.existsSync() ?? false) {
      await logFile?.delete();
    }
    LogManager.logFile = logFile;
    LogManager.addLog(LogLevel.info, "App Status", "Start initialization.");
    await appdata.readData();
    SingleInstanceCookieJar("${App.dataPath}/cookies.db");
    if (kIsWeb) {
      // 先恢复服务端账号态（源凭据/Cookie/相关设置），再初始化漫画源与
      // 各网络层——reLogin 依赖这里写入的数据。
      await GatewaySync.instance.restore();
    }
    if (!kIsWeb) {
      HttpProxyServer.createConfigFile();
      if (appdata.settings[58] == "1") {
        HttpProxyServer.startServer();
      }
      startClearCache();
    }
    if (App.isAndroid) {
      final appLinks = AppLinks();
      appLinks.allUriLinkStream.listen((uri) async {
        while(App.mainNavigatorKey == null) {
          await Future.delayed(const Duration(milliseconds: 100));
        }
        handleAppLinks(uri);
      });
    }
    if (App.isMobile) {
      Workmanager().initialize(
        onStart,
      );
    }
    await checkDownloadPath();
    await _checkOldData();

    // QuickJS is unavailable on web: the JsEngine stub needs no bootstrap and
    // JavaScript comic sources are not loaded.
    if (!kIsWeb) {
      await JsEngine().init();
    }

    await ComicSource.init();

    await Future.wait([
      downloadManager.init(),
      NhentaiNetwork().init(),
      JmNetwork().init(),
      LocalFavoritesManager().init(),
      HistoryManager().init(),
      AppTranslation.init(),
    ]);
    if (kIsWeb) {
      // manager 建表后恢复收藏/历史（删除可传播的整库同步）
      await GatewaySync.instance.restoreData();
    }
    CacheManager().setLimitSize(appdata.appSettings.cacheLimit);
  } catch (e, s) {
    LogManager.addLog(
        LogLevel.error, "Init", "App initialization failed!\n$e$s");
  }
}

Future<void> _checkOldData() async {
  // The browser has no real filesystem, so none of these migrations apply.
  if (kIsWeb) {
    return;
  }
  try {
    if (int.parse(appdata.settings[17]) >= 4) {
      appdata.settings[17] = '0';
    }
    if (int.parse(appdata.settings[40]) > 40) {
      appdata.settings[40] = '40';
    }
    appdata.blockingKeyword.removeWhere((value) => value.isEmpty);

    if (io.Directory("${App.dataPath}/comic_source/cookies/").existsSync() ||
        io.Directory("${App.dataPath}/eh_cookies").existsSync() ||
        io.Directory("${App.dataPath}/comic_source/cookies").existsSync()) {
      // cookies, old version use package cookie_jar
      final cookieJars = [
        PersistCookieJar(storage: FileStorage("${App.dataPath}/cookies")),
        PersistCookieJar(storage: FileStorage("${App.dataPath}/eh_cookies")),
        PersistCookieJar(
            storage: FileStorage("${App.dataPath}/comic_source/cookies/"))
      ];
      var cookies = <io.Cookie>[];
      for (var cookie in (await cookieJars[0]
          .loadForRequest(Uri.parse("https://nhentai.net")))) {
        cookie.domain ??= ".nhentai.net";
        cookies.add(cookie);
      }
      for (var cookie in (await cookieJars[1]
          .loadForRequest(Uri.parse("https://e-hentai.org")))) {
        cookie.domain ??= ".e-hentai.org";
        cookies.add(cookie);
      }
      for (var cookie in (await cookieJars[1]
          .loadForRequest(Uri.parse("https://exhentai.org")))) {
        cookie.domain ??= ".exhentai.org";
        cookies.add(cookie);
      }
      try {
        for (var file in io.Directory("${App.dataPath}/comic_source/cookies/")
            .listSync()) {
          var domain = file.path.split("/").last;
          if (domain == '.domains' || domain == '.index') {
            continue;
          }
          if (domain.startsWith('.')) {
            domain = domain.substring(1);
          }
          for (var cookie in (await cookieJars[2]
              .loadForRequest(Uri.parse("https://$domain")))) {
            cookie.domain ??= ".$domain";
            cookies.add(cookie);
          }
        }
      } finally {}
      if (io.Directory("${App.dataPath}/cookies").existsSync()) {
        io.Directory("${App.dataPath}/cookies").deleteSync(recursive: true);
      }
      if (io.Directory("${App.dataPath}/eh_cookies").existsSync()) {
        io.Directory("${App.dataPath}/eh_cookies").deleteSync(recursive: true);
      }
      if (io.Directory("${App.dataPath}/comic_source/cookies").existsSync()) {
        io.Directory("${App.dataPath}/comic_source/cookies")
            .deleteSync(recursive: true);
      }
    }

    if (io.File("${App.dataPath}/cache.json").existsSync()) {
      io.File("${App.dataPath}/cache.json").deleteIgnoreError();
    }
    if (io.Directory("${App.cachePath}/imageCache").existsSync()) {
      io.Directory("${App.cachePath}/imageCache")
          .deleteIgnoreError(recursive: true);
    }
    if (io.Directory("${App.cachePath}/cachedNetwork").existsSync()) {
      io.Directory("${App.cachePath}/cachedNetwork")
          .deleteIgnoreError(recursive: true);
    }
    await _checkAccountData();
  } catch (e, s) {
    LogManager.addLog(LogLevel.error, "Init", "Check old data failed!\n$e$s");
  }
}

Future<void> _checkAccountData() async {
  var s = await SharedPreferences.getInstance();
  if (s.getString('picacgAccount') != null) {
    var account = s.getString('picacgAccount');
    var pwd = s.getString('picacgPassword');
    var token = s.getString('token');
    picacg.data['account'] = [account, pwd];
    picacg.data['token'] = token;
    picacg.data['user'] = Profile(
      s.getString("userId") ?? "",
      s.getString("userAvatar") ?? '',
      s.getString("userEmail") ?? "",
      s.getInt("userExp") ?? 0,
      s.getInt("userLevel") ?? 0,
      s.getString("userName") ?? "",
      s.getString("userTitle") ?? "",
      false,
      '',
      '',
    ).toJson();
    picacg.data['appChannel'] = s.getString("appChannel") ?? "3";
    picacg.data['imageQuality'] = s.getString('image') ?? "original";
    await picacg.saveData();
    await s.remove('picacgAccount');
  }
  if(s.getString("jmName") != null) {
    var account = s.getString('jmName');
    var pwd = s.getString('jmPwd');
    jm.data['account'] = [account, pwd];
    jm.data['name'] = account;
    await s.remove("jmName");
    await jm.saveData();
  }
  if(s.getString("ehAccount") != null) {
    ehentai.data['account'] = 'ok';
    ehentai.data['name'] = s.getString("ehAccount")!;
    await s.remove("ehAccount");
    await ehentai.saveData();
  }
  if(s.getString('htName') != null) {
    var account = s.getString('htName');
    var pwd = s.getString('htPwd');
    htManga.data['account'] = [account, pwd];
    htManga.data['name'] = account;
    await s.remove('htName');
    await htManga.saveData();
  }
  NhentaiNetwork().init();
  if(NhentaiNetwork().logged) {
    nhentai.data['account'] = 'ok';
    await nhentai.saveData();
  }
}
