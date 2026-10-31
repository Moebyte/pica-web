import 'package:flutter/material.dart';
import 'package:pica_comic/foundation/log.dart';
import 'package:pica_comic/network/hitomi_network/hitomi_main_network.dart';
import 'package:pica_comic/tools/translations.dart';
import '../../foundation/app.dart';
import '../../network/hitomi_network/hitomi_models.dart';
import 'package:pica_comic/components/components.dart';

class HitomiHomePageLogic extends StateController {
  bool loading = true;
  String? message;
  int currentPage = 1;
  ComicList? comics;
  List<HitomiComicBrief> hitomiComics = [];

  /// build() 里会调用 get()/loadNextPage()，加标记避免同一时刻重复请求
  bool _fetching = false;
  bool _loadingNext = false;

  void get(String url) async {
    if (_fetching) return;
    _fetching = true;
    var res = await HiNetwork().getComics(url);
    if (res.error) {
      message = res.errorMessage ?? "Network Error";
      loading = false;
      _fetching = false;
      update();
      return;
    }
    // 先保存列表：parseIds 取到第一批简介后即可结束 loading，
    // 让首屏卡片尽早出现，剩余画廊信息在后台继续加载。
    comics = res.data;
    var parseRes = await parseIds(comics!, firstLoad: true);
    if (!parseRes && hitomiComics.isEmpty) {
      message = "加载失败，请重试";
    }
    loading = false;
    _fetching = false;
    update();
  }

  void loadNextPage(String url) async {
    // 首批简介仍在解析时不要并发翻页：两个 parseIds 会同时消费
    // comics.comicIds，导致重复/丢失条目。
    if (comics == null || _fetching || _loadingNext) return;
    _loadingNext = true;
    var res = await HiNetwork().loadNextPage(comics!);
    if (res.error) {
      showToast(message: res.errorMessage!);
    } else {
      var parseRes = await parseIds(comics!);
      if (parseRes) {
        update();
      } else if (hitomiComics.isEmpty) {
        showToast(message: message ?? "Error");
      }
    }
    _loadingNext = false;
  }

  /// 分批拉取画廊简介。
  ///
  /// 单个 galleryblock 失败（hitomi 边缘节点经常超时）不再让整页失败，
  /// 只跳过该条目；首批成功后立即通知 UI，剩余批次继续填充。
  Future<bool> parseIds(ComicList comics, {bool firstLoad = false}) async {
    var failed = 0;
    var added = 0;
    while (comics.comicIds.isNotEmpty) {
      var batch = comics.comicIds.take(5).toList();
      var result = await Future.wait(
          batch.map((id) => HiNetwork().getComicInfoBrief(id.toString())));
      for (var r in result) {
        if (r.error) {
          failed++;
          LogManager.addLog(LogLevel.warning, "Hitomi",
              "Failed to load gallery brief: ${r.errorMessage}");
          continue;
        }
        hitomiComics.add(r.data);
        added++;
      }
      comics.comicIds.removeRange(0, batch.length);
      if (firstLoad && added > 0) {
        loading = false;
        update();
      }
    }
    return added > 0 || failed == 0;
  }

  void refresh_() {
    loading = true;
    comics = null;
    message = null;
    currentPage = 1;
    hitomiComics.clear();
    _fetching = false;
    _loadingNext = false;
    update();
  }
}

class HitomiHomePageComics extends StatelessWidget {
  const HitomiHomePageComics(this.url, {Key? key}) : super(key: key);
  final String url;

  static void Function() refresh = () {};

  @override
  Widget build(BuildContext context) {
    return StateBuilder<HitomiHomePageLogic>(
        tag: url,
        init: HitomiHomePageLogic(),
        builder: (logic) {
          refresh = logic.refresh_;
          if (logic.loading) {
            logic.get(url);
            return const Center(
              child: CircularProgressIndicator(),
            );
          } else if (logic.message != null) {
            return NetworkError(
              message: logic.message!,
              retry: () => logic.refresh_(),
              withAppbar: false,
            );
          } else {
            return CustomScrollView(
              slivers: [
                SliverGridComics(
                  comics: logic.hitomiComics,
                  sourceKey: "hitomi",
                  onLastItemBuild: () {
                    logic.loadNextPage(url);
                  },
                ),
                if (logic.comics!.toLoad < logic.comics!.total)
                  const SliverToBoxAdapter(
                    child: ListLoadingIndicator(),
                  )
              ],
            );
          }
        });
  }
}

class HitomiHomePage extends StatefulWidget {
  const HitomiHomePage({super.key});

  @override
  State<HitomiHomePage> createState() => _HitomiHomePageState();
}

class _HitomiHomePageState extends State<HitomiHomePage> {
  var type = "index";
  var lang = "-all";

  @override
  Widget build(BuildContext context) {
    var url = "https://ltn.${HiNetwork().baseDomain}/$type$lang.nozomi";
    return Column(
      children: [
        Material(
          textStyle: Theme.of(context).textTheme.headlineMedium,
          child: SizedBox(
            height: 50,
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.end,
              children: [
                const SizedBox(
                  width: 16,
                ),
                const Padding(
                  padding: EdgeInsets.only(bottom: 4),
                  child: Text("hitomi"),
                ),
                const Spacer(),
                Material(
                  child: Select(
                    values: [
                      "最新".tl,
                      "热门 | 今天".tl,
                      "热门 | 一周".tl,
                      "热门 | 本月".tl,
                      "热门 | 一年".tl
                    ],
                    initialValue: 0,
                    onChange: (i) => setState(() {
                      type = [
                        "index",
                        "popular/today",
                        "popular/week",
                        "popular/month",
                        "popular/year"
                      ][i];
                    }),
                  ),
                ),
                const SizedBox(
                  width: 16,
                ),
                Material(
                  child: Select(
                    width: 100,
                    values: const ["All", "中文", "日本語", "English"],
                    initialValue: 0,
                    onChange: (i) => setState(() {
                      lang = ["-all", "-chinese", "-japanese", "-english"][i];
                    }),
                  ),
                ),
                const SizedBox(
                  width: 16,
                ),
              ],
            ),
          ),
        ),
        const Divider(),
        Expanded(
          child: HitomiHomePageComics(
            url,
            key: Key(url),
          ),
        )
      ],
    );
  }
}
