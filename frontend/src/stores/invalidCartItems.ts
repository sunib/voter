/** What in a cart cannot be ordered from this menu: gone from it, or disabled. */
export function invalidCartItems(
  items: Record<string, number>,
  menu: readonly { sku: string; enabled: boolean }[],
): { sku: string; quantity: number }[] {
  const products = new Map(menu.map((product) => [product.sku, product]))
  return Object.entries(items)
    .filter(([sku, quantity]) => quantity > 0 && !products.get(sku)?.enabled)
    .map(([sku, quantity]) => ({ sku, quantity }))
}
