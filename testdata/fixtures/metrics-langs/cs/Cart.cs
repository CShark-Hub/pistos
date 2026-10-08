using System;

namespace Shop;

public class Cart
{
    public int Total(int[] items, int discount = 0)
    {
        var result = 0;
        foreach (var item in items)
        {
            if (item > 100 && item % 2 == 0)
                result += item;
            else if (item > 50)
                result += item / 2;
            else
            {
                try
                {
                    result += checked(item * 2);
                }
                catch (OverflowException)
                {
                    result += 0;
                }
            }
        }
        var label = items.Length > 0 ? "some" : null;
        return result + (label?.Length ?? discount);
    }

    public string Pick(int x) => x switch { 1 => "one", 2 => "two", _ => "other" };

    public int Outer(int a)
    {
        int Inner(int b) => b * 2;
        return Inner(a);
    }
}
