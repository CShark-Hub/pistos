interface Item { price: number; qty?: number }

function parse(n: number): number {
  return n;
}

export class Cart {
  total(items: Item[], discount = 0): number {
    let result = 0;
    for (const item of items) {
      if (item.price > 100 && item.qty) {
        result += item.price;
      } else if (item.price > 50) {
        result += item.price / 2;
      } else {
        try {
          result += parse(item.price);
        } catch (e) {
          result += 0;
        }
      }
    }
    const values = items.filter((v) => v.qty ?? 0);
    return result > 0 ? result : discount;
  }
}

export const pick = (x: number): string => {
  switch (x) {
    case 1:
      return "one";
    case 2:
      return "two";
    default:
      return "other";
  }
};

export function outer(a: number): number {
  function inner(b: number): number {
    return b * 2;
  }
  return inner(a);
}
