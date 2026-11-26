import { IconButton } from "@iquji-community/web-component";
import { IqujiIcon, type IqujiIconName } from "@iquji-community/web-icons";
import { type ReactNode, useEffect, useState } from "react";
import "./notice-carousel.css";

export interface NoticeItem {
  id: string;
  icon?: IqujiIconName;
  tone?: "neutral" | "info" | "warning" | "positive";
  content: ReactNode;
}

export interface NoticeCarouselProps {
  items: NoticeItem[];
  interval?: number;
  autoPlay?: boolean;
  className?: string;
  ariaLabel?: string;
}

export function NoticeCarousel({
  items,
  interval = 4000,
  autoPlay = true,
  className = "",
  ariaLabel = "系统通知轮播",
}: NoticeCarouselProps) {
  const [index, setIndex] = useState(0);
  const [isHovered, setIsHovered] = useState(false);

  const total = items.length;

  useEffect(() => {
    if (index >= total && total > 0) {
      setIndex(0);
    }
  }, [index, total]);

  useEffect(() => {
    if (!autoPlay || total <= 1 || isHovered) return;

    const timer = setInterval(() => {
      setIndex((prev) => (prev + 1) % total);
    }, interval);

    return () => clearInterval(timer);
  }, [autoPlay, total, interval, isHovered]);

  if (total === 0) return null;

  const current = items[Math.min(index, total - 1)] ?? items[0];
  if (!current) return null;

  const handlePrev = () => {
    setIndex((prev) => (prev - 1 + total) % total);
  };

  const handleNext = () => {
    setIndex((prev) => (prev + 1) % total);
  };

  return (
    <section
      aria-label={ariaLabel}
      className={`ops-notice-carousel ${className}`}
      onMouseEnter={() => setIsHovered(true)}
      onMouseLeave={() => setIsHovered(false)}
    >
      <div className="ops-notice-carousel__body" key={current.id}>
        {current.icon ? (
          <span className="ops-notice-carousel__icon" data-tone={current.tone ?? "neutral"}>
            <IqujiIcon decorative name={current.icon} size={14} />
          </span>
        ) : null}
        <div aria-live="polite" className="ops-notice-carousel__content">
          {current.content}
        </div>
      </div>

      {total > 1 ? (
        <nav aria-label="轮播切换" className="ops-notice-carousel__nav">
          <IconButton
            compact
            className="ops-notice-carousel__btn"
            icon={<IqujiIcon decorative name="chevron-left" size={12} />}
            label="上一条"
            onClick={handlePrev}
          />
          <span className="ops-notice-carousel__indicator">
            {index + 1}/{total}
          </span>
          <IconButton
            compact
            className="ops-notice-carousel__btn"
            icon={<IqujiIcon decorative name="chevron-right" size={12} />}
            label="下一条"
            onClick={handleNext}
          />
        </nav>
      ) : null}
    </section>
  );
}
