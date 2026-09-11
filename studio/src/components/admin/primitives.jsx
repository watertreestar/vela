import { Loader2 } from "lucide-react";
import * as React from "react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge as UiBadge } from "@/components/ui/badge";
import { Button as UiButton } from "@/components/ui/button";
import {
  Card as UiCard,
  CardAction as UiCardAction,
  CardContent as UiCardContent,
  CardDescription as UiCardDescription,
  CardHeader as UiCardHeader,
  CardTitle as UiCardTitle,
} from "@/components/ui/card";
import { Checkbox as UiCheckbox } from "@/components/ui/checkbox";
import {
  Dialog as UiDialog,
  DialogClose as UiDialogClose,
  DialogContent as UiDialogContent,
  DialogDescription as UiDialogDescription,
  DialogTitle as UiDialogTitle,
} from "@/components/ui/dialog";
import {
  Field as UiField,
  FieldContent as UiFieldContent,
  FieldDescription as UiFieldDescription,
  FieldError as UiFieldError,
  FieldGroup as UiFieldGroup,
  FieldLabel as UiFieldLabel,
  FieldLegend as UiFieldLegend,
  FieldSeparator as UiFieldSeparator,
  FieldSet as UiFieldSet,
  FieldTitle as UiFieldTitle,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Select as UiSelect,
  SelectContent as UiSelectContent,
  SelectGroup,
  SelectItem as UiSelectItem,
  SelectTrigger as UiSelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Separator as UiSeparator } from "@/components/ui/separator";
import {
  Table as UiTable,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Tabs as UiTabs,
  TabsContent as UiTabsContent,
  TabsList as UiTabsList,
  TabsTrigger as UiTabsTrigger,
} from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import {
  Tooltip as UiTooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

const spacing = {
  0: "0",
  1: "0.25rem",
  2: "0.5rem",
  3: "0.75rem",
  4: "1rem",
  5: "1.25rem",
  6: "1.5rem",
};

function space(value) {
  return spacing[value] || value;
}

function marginStyle(props) {
  const style = { ...props.style };
  if (props.mt) style.marginTop = space(props.mt);
  if (props.mb) style.marginBottom = space(props.mb);
  if (props.pt) style.paddingTop = space(props.pt);
  if (props.pb) style.paddingBottom = space(props.pb);
  if (props.minWidth) style.minWidth = props.minWidth;
  return style;
}

function stripLayoutProps(props) {
  const { mt, mb, pt, pb, minWidth, ...rest } = props;
  return rest;
}

function Box({ as: Comp = "div", className, ...props }) {
  return <Comp className={className} style={marginStyle(props)} {...stripLayoutProps(props)} />;
}

function Flex({
  as: Comp = "div",
  align,
  justify,
  gap,
  wrap,
  direction,
  className,
  ...props
}) {
  return (
    <Comp
      className={cn(
        "flex",
        direction === "column" && "flex-col",
        wrap === "wrap" && "flex-wrap",
        align === "center" && "items-center",
        align === "start" && "items-start",
        align === "end" && "items-end",
        justify === "between" && "justify-between",
        justify === "center" && "justify-center",
        justify === "end" && "justify-end",
        className,
      )}
      style={{ gap: space(gap), ...marginStyle(props) }}
      {...stripLayoutProps(props)}
    />
  );
}

function Grid({ as: Comp = "div", gap, className, ...props }) {
  return (
    <Comp
      className={cn("grid", className)}
      style={{ gap: space(gap), ...marginStyle(props) }}
      {...stripLayoutProps(props)}
    />
  );
}

function Text({ as: Comp = "span", size, color, weight, className, ...props }) {
  return (
    <Comp
      className={cn(
        "admin-text",
        size && `admin-text-size-${size}`,
        color && `admin-text-color-${color}`,
        weight && `admin-text-weight-${weight}`,
        className,
      )}
      style={marginStyle(props)}
      {...stripLayoutProps(props)}
    />
  );
}

function Heading({ as: Comp = "h2", size, className, ...props }) {
  return (
    <Comp
      className={cn("admin-heading", size && `admin-heading-size-${size}`, className)}
      style={marginStyle(props)}
      {...stripLayoutProps(props)}
    />
  );
}

function Code({ color, className, ...props }) {
  return (
    <code
      className={cn("admin-code", color && `admin-text-color-${color}`, className)}
      {...props}
    />
  );
}

function Link({ className, ...props }) {
  return <a className={cn("admin-link", className)} {...props} />;
}

function Spinner({ size = "2", className }) {
  return (
    <Loader2
      className={cn("admin-spinner", size === "3" && "admin-spinner-lg", className)}
      aria-hidden="true"
    />
  );
}

const Button = React.forwardRef(function Button({ variant, color, size, className, ...props }, ref) {
  const mappedVariant =
    color === "red"
      ? "destructive"
      : variant === "ghost"
        ? "ghost"
        : variant === "soft" || variant === "outline"
          ? "outline"
          : "default";
  const mappedSize = size === "1" ? "sm" : size === "3" ? "lg" : "default";
  return (
    <UiButton
      ref={ref}
      variant={mappedVariant}
      size={mappedSize}
      className={cn(color && `admin-button-color-${color}`, className)}
      {...props}
    />
  );
});

function Card({ className, ...props }) {
  return <UiCard className={cn(className)} {...props} />;
}

Card.Header = UiCardHeader;
Card.Title = UiCardTitle;
Card.Description = UiCardDescription;
Card.Action = UiCardAction;
Card.Content = UiCardContent;

function Badge({ color, variant, className, ...props }) {
  const mappedVariant = color === "red" ? "destructive" : variant === "outline" ? "outline" : "secondary";
  return (
    <UiBadge
      variant={mappedVariant}
      className={cn(color && `admin-badge-color-${color}`, className)}
      {...props}
    />
  );
}

function Checkbox(props) {
  return <UiCheckbox {...props} />;
}

function Separator({ size, className, ...props }) {
  return <UiSeparator className={cn(size && `admin-separator-size-${size}`, className)} {...props} />;
}

function TextArea({ className, resize, ...props }) {
  return (
    <Textarea
      className={cn("admin-textarea", resize === "vertical" && "resize-y", className)}
      {...props}
    />
  );
}

function TextFieldRoot({ className, children, ...props }) {
  if (!children) {
    return <Input className={cn("admin-input", className)} {...props} />;
  }
  return (
    <label className={cn("admin-input-group", className)}>
      {children}
      <Input className="admin-input admin-input-in-group" {...props} />
    </label>
  );
}

function TextFieldSlot({ className, ...props }) {
  return <span className={cn("admin-input-slot", className)} {...props} />;
}

const TextField = {
  Root: TextFieldRoot,
  Slot: TextFieldSlot,
};

const Field = {
  Root: UiField,
  Label: UiFieldLabel,
  Description: UiFieldDescription,
  Error: UiFieldError,
  Group: UiFieldGroup,
  Legend: UiFieldLegend,
  Separator: UiFieldSeparator,
  Set: UiFieldSet,
  Content: UiFieldContent,
  Title: UiFieldTitle,
};

function SelectTrigger({ children, className, ...props }) {
  return (
    <UiSelectTrigger className={cn("admin-select-trigger", className)} {...props}>
      {children || <SelectValue />}
    </UiSelectTrigger>
  );
}

function SelectContent({ children, ...props }) {
  return (
    <UiSelectContent {...props}>
      <SelectGroup>{children}</SelectGroup>
    </UiSelectContent>
  );
}

const Select = {
  Root: UiSelect,
  Trigger: SelectTrigger,
  Content: SelectContent,
  Item: UiSelectItem,
};

function TableRoot({ variant, ...props }) {
  return <UiTable {...props} />;
}

const Table = {
  Root: TableRoot,
  Header: TableHeader,
  Body: TableBody,
  Row: TableRow,
  ColumnHeaderCell: TableHead,
  Cell: TableCell,
};

const Tabs = {
  Root: UiTabs,
  List: UiTabsList,
  Trigger: UiTabsTrigger,
  Content: UiTabsContent,
};

const Dialog = {
  Root: UiDialog,
  Content: UiDialogContent,
  Title: UiDialogTitle,
  Description: UiDialogDescription,
  Close({ children, ...props }) {
    return (
      <UiDialogClose asChild={React.isValidElement(children)} {...props}>
        {children}
      </UiDialogClose>
    );
  },
};

const Callout = {
  Root({ color, className, ...props }) {
    return (
      <Alert
        className={cn(color && `admin-alert-color-${color}`, className)}
        variant={color === "red" ? "destructive" : "default"}
        {...props}
      />
    );
  },
  Icon({ className, ...props }) {
    return <span className={cn("admin-alert-icon", className)} {...props} />;
  },
  Text({ className, ...props }) {
    return <AlertDescription className={cn("admin-alert-text", className)} {...props} />;
  },
};

function Tooltip({ content, children }) {
  return (
    <UiTooltip>
      <TooltipTrigger asChild>{children}</TooltipTrigger>
      <TooltipContent>{content}</TooltipContent>
    </UiTooltip>
  );
}

export {
  Badge,
  Box,
  Button,
  Callout,
  Card,
  Checkbox,
  Code,
  Dialog,
  Field,
  Flex,
  Grid,
  Heading,
  Link,
  Select,
  Separator,
  Spinner,
  Table,
  Tabs,
  Text,
  TextArea,
  TextField,
  Tooltip,
  TooltipProvider,
};
