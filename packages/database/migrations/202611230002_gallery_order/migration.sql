-- Supports newest-first paging and bounded scans for a card's Bangkok-day number.
CREATE INDEX "Image_gallery_order_idx" ON "Image" ("lineEventTimestamp" DESC, id ASC);
