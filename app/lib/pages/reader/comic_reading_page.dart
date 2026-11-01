library pica_reader;

import 'dart:async';
import 'dart:io';

import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/scheduler.dart';
import 'package:flutter/services.dart';
import 'package:photo_view/photo_view.dart';
import 'package:photo_view/photo_view_gallery.dart';
import 'package:pica_comic/comic_source/comic_source.dart';
import 'package:pica_comic/components/components.dart';
import 'package:pica_comic/components/custom_slider.dart';
import 'package:pica_comic/components/image_actions.dart';
import 'package:pica_comic/components/long_press_listener.dart';
import 'package:pica_comic/components/scrollable_list/src/item_positions_listener.dart';
import 'package:pica_comic/components/scrollable_list/src/scrollable_positioned_list.dart';
import 'package:pica_comic/components/window_frame.dart';
import 'package:pica_comic/foundation/image_loader/base_image_provider.dart';
import 'package:pica_comic/foundation/image_loader/file_image_loader.dart';
import 'package:pica_comic/foundation/image_loader/image_bytes.dart';
import 'package:pica_comic/foundation/image_loader/stream_image_provider.dart';
import 'package:pica_comic/foundation/local_favorites.dart';
import 'package:pica_comic/network/download.dart';
import 'package:pica_comic/network/eh_network/eh_models.dart';
import 'package:pica_comic/network/eh_network/get_gallery_id.dart';
import 'package:pica_comic/base.dart';
import 'package:pica_comic/network/htmanga_network/htmanga_main_network.dart';
import 'package:pica_comic/network/jm_network/jm_models.dart';
import 'package:pica_comic/network/nhentai_network/nhentai_main_network.dart';
import 'package:pica_comic/network/res.dart';
import 'package:pica_comic/pages/comic_page.dart';
import 'package:pica_comic/tools/keep_screen_on.dart';
import 'package:pica_comic/foundation/image_manager.dart';
import 'package:pica_comic/foundation/history.dart';
import 'package:pica_comic/foundation/nav_snapshot.dart';
import 'package:pica_comic/foundation/web_runtime.dart';
import 'package:pica_comic/tools/save_image.dart';
import 'package:pica_comic/tools/time.dart';
import 'package:pica_comic/network/jm_network/jm_network.dart';
import '../../foundation/app.dart';
import '../../foundation/ui_mode.dart';
import '../../network/hitomi_network/hitomi_models.dart';
import '../../tools/key_down_event.dart';
import 'package:pica_comic/network/picacg_network/methods.dart';
import 'package:pica_comic/tools/translations.dart';

import '../jm/jm_comments_page.dart';

part 'eps_view.dart';

part 'image_view.dart';

part 'image.dart';

part 'touch_control.dart';

part 'reading_logic.dart';

part 'tool_bar.dart';

part 'reading_type.dart';

part 'reading_settings.dart';

part 'reading_data.dart';

/// Bookkeeping for the reader's navigation snapshot entry and page lifecycle
/// listener. `ComicReadingPage` is immutable, so mutable handles live here.
class _NavReaderState {
  Object? token;

  void Function(bool hidden)? lifecycleListener;

  bool Function(KeyEvent event)? keyHandler;
}

///阅读器
class ComicReadingPage extends StatelessWidget {
  final GlobalKey<ScaffoldState> _scaffoldKey = GlobalKey<ScaffoldState>();

  final ReadingData readingData;

  late final History? history = HistoryManager().findSync(readingData.id);

  final int initialPage;

  final int initialEp;

  /// Mutable bookkeeping for the web navigation snapshot / page lifecycle.
  /// Kept in a holder so this widget stays immutable.
  final _NavReaderState _nav = _NavReaderState();

  ReadingType get type => readingData.type;

  ComicReadingPage(this.readingData, this.initialPage, this.initialEp,
      {super.key}) {
    StateController.put(ComicReadingPageLogic(
        initialEp,
        readingData,
        initialPage,
        () => _updateHistory(
            StateController.find<ComicReadingPageLogic>(), false)));
  }

  ComicReadingPage.picacg(
      String target, this.initialEp, List<String> eps, String title,
      {super.key, this.initialPage = 1})
      : readingData = PicacgReadingData(title, target, eps) {
    StateController.put(ComicReadingPageLogic(
        initialEp,
        readingData,
        initialPage,
        () => _updateHistory(
            StateController.find<ComicReadingPageLogic>(), false)));
  }

  ComicReadingPage.ehentai(Gallery gallery, {super.key, this.initialPage = 1})
      : initialEp = 1,
        readingData = EhReadingData(gallery) {
    StateController.put(ComicReadingPageLogic(
        1,
        readingData,
        initialPage,
        () => _updateHistory(
            StateController.find<ComicReadingPageLogic>(), false)));
  }

  ComicReadingPage.jmComic(JmComicInfo comic, this.initialEp,
      {super.key, this.initialPage = 1})
      : readingData = JmReadingData(
          comic.name,
          comic.id,
          comic.series.values.toList(),
          comic.epNames,
        ) {
    StateController.put(ComicReadingPageLogic(
        initialEp,
        readingData,
        initialPage,
        () => _updateHistory(
            StateController.find<ComicReadingPageLogic>(), false)));
  }

  ComicReadingPage.hitomi(HitomiComic comic, String link,
      {super.key, this.initialPage = 1})
      : initialEp = 1,
        readingData = HitomiReadingData(
          comic.title,
          comic.target,
          comic.files,
          link,
        ) {
    StateController.put(ComicReadingPageLogic(
        initialEp,
        readingData,
        initialPage,
        () => _updateHistory(
            StateController.find<ComicReadingPageLogic>(), false)));
  }

  ComicReadingPage.htmanga(String target, String title,
      {super.key, this.initialPage = 1})
      : initialEp = 1,
        readingData = HtReadingData(title, target) {
    StateController.put(ComicReadingPageLogic(
        initialEp,
        readingData,
        initialPage,
        () => _updateHistory(
            StateController.find<ComicReadingPageLogic>(), false)));
  }

  ComicReadingPage.nhentai(String target, String title,
      {super.key, this.initialPage = 1})
      : initialEp = 1,
        readingData = NhentaiReadingData(title, target) {
    StateController.put(ComicReadingPageLogic(
        initialEp,
        readingData,
        initialPage,
        () => _updateHistory(
            StateController.find<ComicReadingPageLogic>(), false)));
  }

  _updateHistory(ComicReadingPageLogic? logic, bool updateMePage) {
    if (readingData.hasEp) {
      if (logic!.order == 1 && logic.index == 1) {
        history?.ep = 0;
        history?.page = 0;
      } else {
        if (logic.order == readingData.eps?.length &&
            logic.index == logic.length) {
          history?.ep = 0;
          history?.page = 0;
        } else {
          history?.ep = logic.order;
          history?.page = logic.index;
        }
      }
    } else {
      if (logic!.index == 1) {
        history?.ep = 0;
        history?.page = 0;
      } else {
        history?.ep = 1;
        history?.page = logic.index;
      }
    }
    history!.maxPage = logic.length;
    HistoryManager().saveReadHistory(history!, updateMePage);
    NavSnapshot.update(_nav.token, {
      "ep": logic.order,
      "page": logic.index,
    });
  }

  bool get useDarkBackground => appdata.appSettings.useDarkBackground;

  @override
  Widget build(BuildContext context) {
    return StateBuilder<ComicReadingPageLogic>(initState: (logic) {
      SystemChrome.setEnabledSystemUIMode(SystemUiMode.immersive);
      _nav.token = NavSnapshot.enter(NavSnapshot.readerData(
        source: readingData.sourceKey,
        id: readingData.id,
        ep: logic.order,
        page: logic.index,
        detailId: readingData is HitomiReadingData
            ? (readingData as HitomiReadingData).link
            : null,
      ));
      // Persist progress when the tab is hidden or the page is being
      // unloaded, before Flutter's dispose callbacks get a chance to run.
      _nav.lifecycleListener = (hidden) {
        if (!hidden) return;
        try {
          if (history != null) {
            _updateHistory(logic, false);
          }
          NavSnapshot.flush();
        } catch (e) {
          // Never let progress saving break the reader.
        }
      };
      addPageLifecycleListener(_nav.lifecycleListener!);
      // PageUp/PageDown are consumed by scrollables in the focused subtree,
      // so handle them (and Esc) at the hardware-keyboard level before focus
      // dispatch. Arrow keys / space stay on the KeyboardListener.
      _nav.keyHandler = (event) {
        try {
          if (event is! KeyDownEvent && event is! KeyRepeatEvent) {
            return false;
          }
          final logic = StateController.findOrNull<ComicReadingPageLogic>();
          if (logic == null || logic.isEditingText) return false;
          switch (event.logicalKey) {
            case LogicalKeyboardKey.pageUp:
              logic.jumpToLastPage();
              return true;
            case LogicalKeyboardKey.pageDown:
              logic.jumpToNextPage();
              return true;
            case LogicalKeyboardKey.escape:
              if (logic.tools) {
                logic.tools = false;
                logic.update();
                SystemChrome.setEnabledSystemUIMode(SystemUiMode.immersive);
              } else {
                App.globalBack();
              }
              return true;
          }
        } catch (_) {
          // Never let a keyboard shortcut break reading.
        }
        return false;
      };
      HardwareKeyboard.instance.addHandler(_nav.keyHandler!);
      if (App.isWeb) {
        // 不再自动全屏：部分手机浏览器进入全屏后会触发方向锁定/旋转，
        // 阅读体验反而更差。全屏改为顶栏/设置里的手动开关。
        unlockScreenOrientation();
      }
      if (appdata.settings[14] == "1") {
        setKeepScreenOn();
      }
      if (!App.isWeb) {
        if (appdata.settings[76] == "1") {
          SystemChrome.setPreferredOrientations([
            DeviceOrientation.landscapeLeft,
            DeviceOrientation.landscapeRight
          ]);
        } else if (appdata.settings[76] == "2") {
          SystemChrome.setPreferredOrientations(
              [DeviceOrientation.portraitUp, DeviceOrientation.portraitDown]);
        }
      }
      //进入阅读器时清除内存中的缓存, 并且增大限制
      BaseImageProvider.clearCache();
      BaseImageProvider.setCacheSizeLimit(100 * 1024 * 1024);
      logic.openEpsView = openEpsDrawer;
      if (useDarkBackground) {
        Future.microtask(() =>
            StateController.findOrNull<WindowFrameController>()
                ?.setDarkTheme());
      }
    }, dispose: (logic) {
      //清除缓存并减小最大缓存
      BaseImageProvider.clearCache();
      BaseImageProvider.setCacheSizeLimit(50 * 1024 * 1024);
      logic.clearPhotoViewControllers();
      SystemChrome.setEnabledSystemUIMode(SystemUiMode.edgeToEdge);
      if (!App.isWeb) {
        SystemChrome.setPreferredOrientations(DeviceOrientation.values);
      }
      if (logic.listenVolume != null) {
        logic.listenVolume!.stop();
      }
      if (appdata.settings[14] == "1") {
        cancelKeepScreenOn();
      }
      logic.runningAutoPageTurning = false;
      ComicImage.clear();
      StateController.remove<ComicReadingPageLogic>();
      // 更新本地收藏
      LocalFavoritesManager()
          .onReadEnd(readingData.favoriteId, readingData.favoriteType);
      // 保存历史记录
      if (history != null) {
        _updateHistory(logic, true);
      }
      // 退出全屏
      if (logic.isFullScreen) {
        logic.fullscreen();
      }
      if (!DownloadManager().isDownloading) {
        ImageManager.clearTasks();
      }
      // 更新漫画详情页面
      Future.microtask(() {
        if (BaseComicPage.tagsStack.isNotEmpty) {
          BaseComicPage.tagsStack.last.updateHistory(history);
        }
      });
      if (!App.isWeb && appdata.settings[76] != "0") {
        SystemChrome.setPreferredOrientations(DeviceOrientation.values);
      }
      if (useDarkBackground) {
        Future.microtask(() =>
            StateController.findOrNull<WindowFrameController>()?.resetTheme());
      }
      if (_nav.keyHandler != null) {
        HardwareKeyboard.instance.removeHandler(_nav.keyHandler!);
        _nav.keyHandler = null;
      }
      if (_nav.lifecycleListener != null) {
        removePageLifecycleListener(_nav.lifecycleListener!);
      }
      NavSnapshot.leave(_nav.token);
      _nav.token = null;
      if (App.isWeb) {
        // Leave the browser fullscreen when closing the reader.
        exitFullscreen();
      }
    }, builder: (logic) {
      return DefaultTextStyle.merge(
        style: TextStyle(
          color: useDarkBackground ? Colors.white : null,
          fontSize: 16,
        ),
        child: Scaffold(
          backgroundColor: useDarkBackground ? Colors.black : null,
          endDrawerEnableOpenDragGesture: false,
          key: _scaffoldKey,
          endDrawer: Drawer(
            child: buildEpsView(),
          ),
          floatingActionButton: buildEpChangeButton(logic),
          body: StateBuilder<ComicReadingPageLogic>(builder: (logic) {
            if (logic.isLoading) {
              history?.readEpisode.add(logic.order);
              loadInfo(logic);
              return ReaderLoadingPlaceholder(
                dark: useDarkBackground,
                onExit: () => Navigator.of(context).maybePop(),
              );
            } else if (logic.urls.isNotEmpty) {
              if (logic.readingMethod ==
                      ReadingMethod.topToBottomContinuously &&
                  !logic.haveUsedInitialPage &&
                  initialPage != 0) {
                Future.microtask(() {
                  logic.jumpToPage(initialPage);
                  logic.haveUsedInitialPage = true;
                });
              }
              //监听音量键
              if (appdata.settings[7] == "1") {
                if (logic.listenVolume == null) {
                  logic.listenVolume = ListenVolumeController(
                      () => logic.jumpToLastPage(),
                      () => logic.jumpToNextPage());
                  logic.listenVolume!.listenVolumeChange();
                }
              } else if (logic.listenVolume != null) {
                logic.listenVolume!.stop();
                logic.listenVolume = null;
              }

              if (appdata.settings[9] == "4") {
                logic.scrollManager ??= ScrollManager(logic);
              }

              var body = LongPressPointerDetector(
                  onLongPress: (position) =>
                      _onReaderLongPress(context, logic, position),
                  child: Listener(
                    onPointerMove: TapController.onPointerMove,
                    onPointerUp: TapController.onTapUp,
                    onPointerDown: TapController.onTapDown,
                    behavior: HitTestBehavior.translucent,
                    onPointerCancel: TapController.onTapCancel,
                    child: Stack(
                      children: [
                        buildComicView(
                          logic,
                          context,
                          readingData.id,
                        ),
                        if (MediaQuery.of(context).platformBrightness ==
                                Brightness.dark &&
                            appdata.appSettings.reduceBrightnessInDarkMode)
                          Positioned(
                            top: 0,
                            bottom: 0,
                            left: 0,
                            right: 0,
                            child: IgnorePointer(
                              child: ColoredBox(
                                color: Colors.black.withOpacity(0.2),
                              ),
                            ),
                          ),

                        if (appdata.appSettings.showPageInfoInReader)
                          buildPageInfoText(logic, context),

                        //底部工具栏
                        buildBottomToolBar(logic, context, readingData.hasEp),

                        ...buildButtons(logic, context),

                        //顶部工具栏
                        buildTopToolBar(logic, context),
                      ],
                    ),
                  ));

              return KeyboardListener(
                focusNode: logic.focusNode,
                autofocus: true,
                onKeyEvent: logic.handleKeyboard,
                child: body,
              );
            } else {
              return buildErrorView(logic, context);
            }
          }),
        ),
      );
    });
  }

  /// 只在图片区域触发长按菜单，避免长按顶栏标题时误弹图片菜单。
  void _onReaderLongPress(
      BuildContext context, ComicReadingPageLogic logic, Offset position) {
    final size = MediaQuery.of(context).size;
    if (position.dy < 84 || position.dy > size.height - 150) {
      return;
    }
    _showImageActions(context, logic);
  }

  /// Web：长按正文图片弹出复制/保存菜单。
  void _showImageActions(BuildContext context, ComicReadingPageLogic logic) {
    if (!App.isWeb || logic.urls.isEmpty) {
      return;
    }
    final pageIndex = logic.index < 1
        ? 0
        : (logic.index > logic.urls.length
            ? logic.urls.length - 1
            : logic.index - 1);
    final url = logic.urls[pageIndex];
    final key = logic.data.buildImageKey(logic.order, pageIndex, url);
    Future<Uint8List?> loadBytes() async {
      final cached = ImageByteCache.get(key);
      if (cached != null) {
        return cached;
      }
      Uint8List? bytes;
      await for (final progress
          in logic.data.loadImage(logic.order, pageIndex, url)) {
        if (progress.data != null) {
          bytes = progress.data;
        }
      }
      return bytes;
    }

    showImageActionsSheet(
      context,
      title: "${"第".tl}${pageIndex + 1}${"页".tl} · ${logic.data.title}",
      fileName:
          "${logic.data.title}-${logic.data.hasEp ? "ep${logic.order}-" : ""}${pageIndex + 1}",
      loadBytes: loadBytes,
    );
  }

  Widget buildErrorView(ComicReadingPageLogic logic, BuildContext context) {
    return SafeArea(
        child: Stack(
      children: [
        Positioned(
          left: 8,
          top: 12,
          child: IconButton(
            icon: const Icon(
              Icons.arrow_back,
            ),
            onPressed: () => App.globalBack(),
          ),
        ),
        Positioned(
          top: MediaQuery.of(App.globalContext!).size.height / 2 - 80,
          left: 0,
          right: 0,
          child: const Align(
            alignment: Alignment.topCenter,
            child: Icon(
              Icons.error_outline,
              size: 60,
            ),
          ),
        ),
        Positioned(
          left: 0,
          right: 0,
          top: MediaQuery.of(App.globalContext!).size.height / 2 - 10,
          child: Align(
            alignment: Alignment.topCenter,
            child: Text(
              logic.errorMessage ?? "未知错误".tl,
            ),
          ),
        ),
        Positioned(
          left: 0,
          right: 0,
          top: MediaQuery.of(App.globalContext!).size.height / 2 + 30,
          child: Align(
            alignment: Alignment.topCenter,
            child: SizedBox(
              width: 250,
              height: 40,
              child: Row(
                children: [
                  Expanded(
                    child: FilledButton(
                      onPressed: () {
                        logic.change();
                      },
                      child: Text("重试".tl),
                    ),
                  ),
                  const SizedBox(
                    width: 8,
                  ),
                  Expanded(
                      child: FilledButton(
                    onPressed: () {
                      if (!readingData.hasEp) {
                        showToast(message: "没有其它章节".tl);
                        return;
                      }
                      if (MediaQuery.of(context).size.width > 600) {
                        showSideBar(
                          context,
                          buildEpsView(),
                          title: null,
                          useSurfaceTintColor: true,
                          addTopPadding: true,
                          width: 400,
                        );
                      } else {
                        showModalBottomSheet(
                          context: context,
                          useSafeArea: false,
                          builder: (context) {
                            return buildEpsView();
                          },
                        );
                      }
                    },
                    child: Text("切换章节".tl),
                  )),
                ],
              ),
            ),
          ),
        ),
      ],
    ));
  }

  /// Web：整章图片签名一次性预取，之后每页只发图片 GET。
  /// 这里等待它完成再结束章节 loading：首图只付一次批量签名 RTT，
  /// 之后所有页都省掉单张 sign 的往返。
  Future<void> _prefetchSignatures(ComicReadingPageLogic logic) async {
    if (!App.isWeb || logic.urls.isEmpty) {
      return;
    }
    final specs = <Map<String, dynamic>>[];
    for (final url in logic.urls) {
      final spec = readingData.signSpec(logic.order, url);
      if (spec == null) {
        continue;
      }
      specs.add({"url": url, ...spec});
    }
    if (specs.isNotEmpty) {
      await ImageManager().primeSignedImages(specs);
    }
  }

  void loadInfo(ComicReadingPageLogic logic) async {
    logic.urls = [];
    var res = await readingData.loadEp(logic.order);
    if (res.error) {
      logic.errorMessage = res.errorMessage;
    } else {
      logic.urls = res.data;
      await _prefetchSignatures(logic);
    }
    logic.isLoading = false;
    logic.update();
  }

  Widget buildEpsView() {
    return EpsView(readingData);
  }

  void openEpsDrawer() {
    var context = App.globalContext!;
    if (MediaQuery.of(context).size.width > 600) {
      showSideBar(
        context,
        buildEpsView(),
        title: null,
        useSurfaceTintColor: true,
        width: 400,
        addTopPadding: true,
      );
    } else {
      showModalBottomSheet(
        context: context,
        useSafeArea: false,
        builder: (context) {
          return buildEpsView();
        },
      );
    }
  }

  /// Used when [ComicReadingPageLogic.readingMethod] is [ReadingMethod.topToBottomContinuously].
  ///
  /// Select a image form screen, to share or download
  Future<int?> selectImage() async {
    var logic = StateController.find<ComicReadingPageLogic>();
    var items = logic.itemScrollListener.itemPositions.value.toList();
    if (items.length == 1) {
      return items[0].index;
    }
    int? res;
    await showDialog(
        context: App.globalContext!,
        builder: (context) {
          return SimpleDialog(
            title: Text("选择屏幕上的图片".tl),
            children: [
              ConstrainedBox(
                constraints: const BoxConstraints(
                  maxWidth: 400,
                ),
                child: Column(
                  children: [
                    for (var item in items)
                      ListTile(
                        title: Text((item.index + 1).toString()),
                        onTap: () {
                          res = item.index;
                          App.globalBack();
                        },
                        trailing: const Icon(Icons.arrow_right),
                      )
                  ],
                ),
              )
            ],
          );
        });
    return res;
  }

  String getImageKey(int index) {
    var logic = StateController.find<ComicReadingPageLogic>();
    if (type == ComicType.ehentai) {
      return "${readingData.id}${index + 1}";
    }
    return type == ReadingType.hitomi
        ? (readingData as HitomiReadingData).images[index].hash
        : logic.urls[index];
  }

  Future<File> _getFileFromStream(Stream<DownloadProgress> stream) async {
    await for (var event in stream) {
      if (event.finished) {
        return event.getFile();
      }
    }
    throw "failed";
  }

  void share() async {
    var logic = StateController.find<ComicReadingPageLogic>();
    int? index = logic.index - 1;
    if (logic.readingMethod == ReadingMethod.topToBottomContinuously) {
      index = await selectImage();
    }
    if (index == null) {
      return;
    }

    var file = await _getFileFromStream(
        readingData.loadImage(logic.order, index, logic.urls[index]));

    shareImage(file);
  }

  Future<String?> _persistentCurrentImage() async {
    var logic = StateController.find<ComicReadingPageLogic>();
    int? index = logic.index - 1;
    if (logic.readingMethod == ReadingMethod.topToBottomContinuously) {
      index = await selectImage();
    }
    if (index == null) {
      return null;
    }

    var file = await _getFileFromStream(
        readingData.loadImage(logic.order, index, logic.urls[index]));

    return persistentCurrentImage(file);
  }

  void saveCurrentImage() async {
    var logic = StateController.find<ComicReadingPageLogic>();
    int? index = logic.index - 1;
    if (logic.readingMethod == ReadingMethod.topToBottomContinuously) {
      index = await selectImage();
    }
    if (index == null) {
      return;
    }

    var file = await _getFileFromStream(
        readingData.loadImage(logic.order, index, logic.urls[index]));

    saveImage(file);
  }

  Widget? buildEpChangeButton(ComicReadingPageLogic logic) {
    if (!readingData.hasEp) return null;
    switch (logic.showFloatingButtonValue) {
      case -1:
        return FloatingActionButton(
          onPressed: () => logic.jumpToLastChapter(),
          child: const Icon(Icons.arrow_back_ios_outlined),
        );
      case 0:
        return null;
      case 1:
        return Hero(
            tag: "FAB",
            child: StateBuilder<ComicReadingPageLogic>(
              id: "FAB",
              builder: (logic) {
                return Container(
                  width: 58,
                  height: 58,
                  clipBehavior: Clip.antiAlias,
                  decoration: BoxDecoration(
                      color: Theme.of(App.globalContext!)
                          .colorScheme
                          .primaryContainer,
                      borderRadius: BorderRadius.circular(16)),
                  child: Stack(
                    children: [
                      Positioned.fill(
                          child: Material(
                        color: Colors.transparent,
                        child: InkWell(
                          onTap: () => logic.jumpToNextChapter(),
                          borderRadius: BorderRadius.circular(16),
                          child: Center(
                              child: Icon(
                            Icons.arrow_forward_ios,
                            size: 24,
                            color: Theme.of(App.globalContext!)
                                .colorScheme
                                .onPrimaryContainer,
                          )),
                        ),
                      )),
                      Positioned(
                        bottom: 0,
                        left: 0,
                        right: 0,
                        height: logic.fABValue,
                        child: ColoredBox(
                          color: Theme.of(App.globalContext!)
                              .colorScheme
                              .surfaceTint
                              .withOpacity(0.2),
                          child: const SizedBox.expand(),
                        ),
                      )
                    ],
                  ),
                );
              },
            ));
    }
    return null;
  }
}

/// 阅读器打开章节时的加载占位：顶部细进度条 + 页面骨架 + 退出按钮，
/// 避免在屏幕正中长时间转圈。
class ReaderLoadingPlaceholder extends StatelessWidget {
  const ReaderLoadingPlaceholder({
    super.key,
    required this.dark,
    required this.onExit,
  });

  final bool dark;
  final VoidCallback onExit;

  @override
  Widget build(BuildContext context) {
    final fg = dark ? Colors.white70 : context.colorScheme.onSurfaceVariant;
    return ColoredBox(
      color: dark ? Colors.black : context.colorScheme.surface,
      child: SafeArea(
        child: Stack(
          children: [
            Positioned(
              left: 0,
              right: 0,
              top: 0,
              child: LinearProgressIndicator(
                minHeight: 2,
                backgroundColor: Colors.transparent,
                color: dark ? Colors.white70 : null,
              ),
            ),
            Positioned(
              left: 4,
              top: 0,
              child: IconButton(
                icon: const Icon(Icons.close),
                color: fg,
                tooltip: "关闭".tl,
                onPressed: onExit,
              ),
            ),
            Center(
              child: FractionallySizedBox(
                widthFactor: 0.62,
                heightFactor: 0.72,
                child: DecoratedBox(
                  decoration: BoxDecoration(
                    color: dark
                        ? Colors.white.withOpacity(0.04)
                        : Colors.black.withOpacity(0.03),
                    borderRadius: BorderRadius.circular(8),
                  ),
                  child: Center(
                    child: Text(
                      "正在加载章节…".tl,
                      style: TextStyle(fontSize: 13, color: fg),
                    ),
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}
