import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "@/test/render";
import { NoticeCarousel } from "./notice-carousel";

describe("NoticeCarousel 消息轮播组件", () => {
  it("无消息时不渲染任何 DOM", () => {
    const html = renderToStaticMarkup(<NoticeCarousel items={[]} />);
    expect(html).toBe("");
  });

  it("单条消息时静态展示且不渲染切换控制器", () => {
    const html = renderToStaticMarkup(
      <NoticeCarousel
        items={[
          {
            id: "msg-1",
            icon: "clock",
            content: "检查于 2026/01/01 12:00:00",
          },
        ]}
      />,
    );
    expect(html).toContain("检查于 2026/01/01 12:00:00");
    expect(html).not.toContain("轮播切换");
  });

  it("多条消息时渲染初始内容与控制器", () => {
    const html = renderToStaticMarkup(
      <NoticeCarousel
        items={[
          { id: "1", content: "第一条提示" },
          { id: "2", content: "第二条提示" },
        ]}
      />,
    );

    expect(html).toContain("第一条提示");
    expect(html).toContain("轮播切换");
    expect(html).toContain("1/2");
    expect(html).toContain("上一条");
    expect(html).toContain("下一条");
  });
});
